#!/bin/sh
# Separate real sessions, synchronized by observed parent-row lock waits, not timing guesses.
# Invoked inside check-meetings.sh's isolated PostgreSQL container only.
set -eu
work=$(mktemp -d)
holder_pid=
writer_pid=
cleanup() {
  [ -z "$holder_pid" ] || kill "$holder_pid" 2>/dev/null || true
  [ -z "$writer_pid" ] || kill "$writer_pid" 2>/dev/null || true
  rm -rf "$work"
}
trap cleanup EXIT HUP INT TERM
sql() { psql -X -h 127.0.0.1 -U postgres -v ON_ERROR_STOP=1 "$@"; }
wait_for_session() {
  session=$1
  condition=$2
  attempt=0
  until [ "$(sql -Atc "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE application_name = '$session' AND $condition)")" = t ]; do
    attempt=$((attempt + 1))
    if [ "$attempt" -gt 200 ]; then
      cat "$work"/*.log
      sql -c "SELECT application_name,state,wait_event_type,wait_event,query FROM pg_stat_activity WHERE application_name LIKE 'transcript_%'"
      echo "session did not reach controlled barrier: $session" >&2
      exit 1
    fi
    sleep 0.05
  done
}
start_holder() {
  statement=$1
  mkfifo "$work/holder.sql"
  PGAPPNAME=transcript_holder sql < "$work/holder.sql" > "$work/holder.log" 2>&1 &
  holder_pid=$!
  exec 3> "$work/holder.sql"
  printf '%s\n' "BEGIN; SET LOCAL ROLE authenticated; SET LOCAL request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555'; $statement" >&3
  wait_for_session transcript_holder "state = 'idle in transaction'"
}
release_holder() {
  printf '%s\n' 'COMMIT;' >&3
  exec 3>&-
  wait "$holder_pid" || { cat "$work/holder.log"; exit 1; }
  holder_pid=
  rm "$work/holder.sql"
}
start_writer() {
  statement=$1
  PGAPPNAME=transcript_writer sql -c "SET ROLE authenticated; SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555'; $statement" 3>&- > "$work/writer.log" 2>&1 &
  writer_pid=$!
  wait_for_session transcript_writer "wait_event_type = 'Lock'"
}
finish_writer() {
  wait "$writer_pid" || { cat "$work/writer.log"; exit 1; }
  writer_pid=
}
sql <<'SQL'
-- Independent active fixtures are required for the three lock orderings below.
-- meeting_runs.sql separately verifies the production one-active-run invariant.
DROP INDEX public.meeting_runs_one_active_owner_idx;
INSERT INTO public.meeting_runs(id,user_id,meeting_id,occurrence_id,save_transcript)
SELECT ids.id, '11111111-2222-4333-8444-555555555555', m.id, o.id, true
FROM public.meeting_sessions m
JOIN LATERAL (
  SELECT id FROM public.meeting_occurrences
  WHERE user_id = m.user_id AND meeting_id = m.id AND status = 'active'
  ORDER BY started_at LIMIT 1
) o ON true
CROSS JOIN (VALUES
  ('b0000000-0000-4000-8000-000000000001'::uuid),
  ('b0000000-0000-4000-8000-000000000002'::uuid),
  ('b0000000-0000-4000-8000-000000000004'::uuid)
) ids(id) WHERE m.user_id = '11111111-2222-4333-8444-555555555555';
SQL
# Delete wins the lock first: already-sent append must re-read terminal state.
start_holder "SELECT * FROM public.delete_transcript('b0000000-0000-4000-8000-000000000001');"
start_writer "DO \$\$ BEGIN IF EXISTS (SELECT 1 FROM public.append_transcript_final('b0000000-0000-4000-8000-000000000001',1,'late race final',0,1,'en')) THEN RAISE EXCEPTION 'append revived deleted run after parent lock wait'; END IF; END \$\$;"
release_holder
finish_writer
# Append wins the lock first: later deletion must remove the committed in-flight final.
start_holder "SELECT * FROM public.append_transcript_final('b0000000-0000-4000-8000-000000000002',1,'in-flight final',0,1,'en');"
start_writer "DO \$\$ BEGIN IF (SELECT transcript_state FROM public.delete_transcript('b0000000-0000-4000-8000-000000000002')) <> 'deleted' THEN RAISE EXCEPTION 'delete failed after append lock wait'; END IF; END \$\$;"
release_holder
finish_writer
# Direct INSERT also waits on exactly the same barrier, then rejects the deleted parent.
start_holder "SELECT * FROM public.delete_transcript('b0000000-0000-4000-8000-000000000004');"
start_writer "DO \$\$ BEGIN BEGIN INSERT INTO public.transcript_segments(user_id,meeting_id,occurrence_id,run_id,seq,text,start,duration,language) SELECT auth.uid(),meeting_id,occurrence_id,id,1,'direct late final',0,1,'en' FROM public.meeting_runs WHERE id='b0000000-0000-4000-8000-000000000004'; RAISE EXCEPTION 'direct insert passed stale parent state'; EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END; END \$\$;"
release_holder
finish_writer
sql <<'SQL'
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id IN (
    'b0000000-0000-4000-8000-000000000001','b0000000-0000-4000-8000-000000000002',
    'b0000000-0000-4000-8000-000000000004')) THEN
    RAISE EXCEPTION 'concurrent deletion left physical text'; END IF;
  IF (SELECT count(*) FROM public.meeting_runs WHERE transcript_state = 'deleted' AND id IN (
    'b0000000-0000-4000-8000-000000000001','b0000000-0000-4000-8000-000000000002',
    'b0000000-0000-4000-8000-000000000004')) <> 3 THEN
    RAISE EXCEPTION 'concurrent delete lost terminal metadata'; END IF;
END $$;
UPDATE public.meeting_runs
SET status = 'stopped', ended_at = COALESCE(ended_at, now())
WHERE id IN (
  'b0000000-0000-4000-8000-000000000001','b0000000-0000-4000-8000-000000000002',
  'b0000000-0000-4000-8000-000000000004');
CREATE UNIQUE INDEX meeting_runs_one_active_owner_idx
  ON public.meeting_runs(user_id)
  WHERE status = 'active';
SELECT 'controlled transcript append/delete concurrency checks passed' AS result;
SQL
