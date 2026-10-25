-- Behavioral regressions: removing any guard must expose an observable violation here.
-- Runs after meeting_runs.sql, in the isolated PostgreSQL harness only.
\i /migrations/0004_transcript_segments.sql
-- This suite needs several simultaneous owner fixtures to exercise independent
-- transcript states. The production invariant is tested in meeting_runs.sql.
DROP INDEX public.meeting_runs_one_active_owner_idx;
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
CREATE TEMP TABLE transcript_test_runs (label text PRIMARY KEY, id uuid, meeting_id uuid);
WITH r AS (
  INSERT INTO public.meeting_runs (user_id, meeting_id)
  SELECT auth.uid(), id FROM public.meeting_sessions RETURNING id, meeting_id
) INSERT INTO transcript_test_runs SELECT 'disabled', id, meeting_id FROM r;
WITH r AS (
  INSERT INTO public.meeting_runs (user_id, meeting_id, save_transcript)
  SELECT auth.uid(), id, true FROM public.meeting_sessions RETURNING id, meeting_id
) INSERT INTO transcript_test_runs SELECT 'saving', id, meeting_id FROM r;
WITH r AS (
  INSERT INTO public.meeting_runs (user_id, meeting_id, save_transcript)
  SELECT auth.uid(), id, true FROM public.meeting_sessions RETURNING id, meeting_id
) INSERT INTO transcript_test_runs SELECT 'empty', id, meeting_id FROM r;
WITH r AS (
  INSERT INTO public.meeting_runs (user_id, meeting_id, save_transcript)
  SELECT auth.uid(), id, true FROM public.meeting_sessions RETURNING id, meeting_id
) INSERT INTO transcript_test_runs SELECT 'failed', id, meeting_id FROM r;
DO $$
DECLARE r public.meeting_runs; segment public.transcript_segments; detail text;
BEGIN
  SELECT * INTO r FROM public.meeting_runs WHERE id = (SELECT id FROM transcript_test_runs WHERE label = 'disabled');
  IF r.save_transcript OR r.transcript_state <> 'disabled' OR r.transcript_expires_at IS NOT NULL THEN
    RAISE EXCEPTION 'default consent did not disable storage';
  END IF;
  IF EXISTS (SELECT 1 FROM public.append_transcript_final(r.id, 1, 'must not persist', 0, 1, 'en')) THEN
    RAISE EXCEPTION 'opt-out append returned a final';
  END IF;
  BEGIN
    INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
    VALUES (auth.uid(), r.meeting_id, r.id, 1, 'must not persist', 0, 1, 'en');
    RAISE EXCEPTION 'opt-out direct insert was accepted';
  EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END;
  SELECT * INTO r FROM public.meeting_runs WHERE id = (SELECT id FROM transcript_test_runs WHERE label = 'saving');
  IF NOT r.save_transcript OR r.transcript_state <> 'saving'
     OR r.transcript_expires_at <> r.started_at + interval '30 days' THEN
    RAISE EXCEPTION 'opt-in expiry/state was not database-derived';
  END IF;
  SELECT * INTO segment FROM public.append_transcript_final(r.id, 3, 'first final', 1.25, 2.5, 'en');
  IF segment.id IS NULL OR segment.seq <> 3 OR segment.text <> 'first final' OR segment.start <> 1.25 OR segment.duration <> 2.5
     OR segment.language <> 'en' OR segment.run_id <> r.id OR segment.meeting_id <> r.meeting_id
     OR segment.user_id <> auth.uid() THEN RAISE EXCEPTION 'accepted final content/identity changed'; END IF;
  IF (SELECT count(*) FROM public.append_transcript_final(r.id, 3, 'first final', 1.25, 2.5, 'en')) <> 1 THEN
    RAISE EXCEPTION 'identical duplicate did not acknowledge existing row';
  END IF;
  IF (SELECT count(*) FROM public.transcript_segments WHERE run_id = r.id) <> 1 THEN
    RAISE EXCEPTION 'identical duplicate created another row';
  END IF;
  BEGIN
    PERFORM * FROM public.append_transcript_final(r.id, 3, 'conflicting private text', 1.25, 2.5, 'en');
    RAISE EXCEPTION 'conflicting final was accepted';
  EXCEPTION WHEN unique_violation THEN
    GET STACKED DIAGNOSTICS detail = PG_EXCEPTION_DETAIL;
    IF position('private text' in SQLERRM || coalesce(detail, '')) > 0 THEN RAISE EXCEPTION 'conflict error leaked transcript'; END IF;
  END;
  IF (SELECT text FROM public.transcript_segments WHERE run_id = r.id AND seq = 3) <> 'first final' THEN
    RAISE EXCEPTION 'conflicting duplicate overwrote final';
  END IF;
  BEGIN
    UPDATE public.transcript_segments SET text = 'rewritten' WHERE run_id = r.id;
    RAISE EXCEPTION 'final text update was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    UPDATE public.meeting_runs SET save_transcript = false WHERE id = r.id;
    RAISE EXCEPTION 'consent update was allowed';
  EXCEPTION WHEN insufficient_privilege OR check_violation THEN NULL; END;
  BEGIN
    UPDATE public.meeting_runs SET transcript_expires_at = clock_timestamp() + interval '60 days' WHERE id = r.id;
    RAISE EXCEPTION 'expiry extension was allowed';
  EXCEPTION WHEN insufficient_privilege OR check_violation THEN NULL; END;
  BEGIN
    INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
    VALUES ('66666666-2222-4333-8444-555555555555', r.meeting_id, r.id, 4, 'private owner mismatch', 0, 1, 'en');
    RAISE EXCEPTION 'segment owner mismatch was accepted';
  EXCEPTION WHEN insufficient_privilege OR check_violation THEN NULL; END;
  BEGIN
    INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
    VALUES (auth.uid(), gen_random_uuid(), r.id, 4, 'private meeting mismatch', 0, 1, 'en');
    RAISE EXCEPTION 'segment meeting mismatch was accepted';
  EXCEPTION WHEN insufficient_privilege OR check_violation THEN NULL; END;
  BEGIN
    PERFORM * FROM public.append_transcript_final(r.id, 4, 'invalid timing private text', 'NaN'::double precision, 1, 'en');
    RAISE EXCEPTION 'nonfinite start was accepted';
  EXCEPTION WHEN check_violation THEN
    GET STACKED DIAGNOSTICS detail = PG_EXCEPTION_DETAIL;
    IF position('private text' in SQLERRM || coalesce(detail, '')) > 0 THEN RAISE EXCEPTION 'validation error leaked transcript'; END IF;
  END;
  BEGIN
    DELETE FROM public.transcript_segments WHERE run_id = r.id;
    RAISE EXCEPTION 'direct segment DELETE was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    DELETE FROM public.transcript_segments WHERE false;
    RAISE EXCEPTION 'empty direct DELETE was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  IF (SELECT count(*) FROM public.transcript_segments WHERE run_id = r.id) <> 1 THEN
    RAISE EXCEPTION 'rejected direct delete removed final';
  END IF;
END $$;
-- Reapplication must preserve saved content and run/capture counters.
SELECT * FROM public.append_audio_chunk((SELECT id FROM transcript_test_runs WHERE label = 'saving'), 25);
SELECT * FROM public.set_transcript_state((SELECT id FROM transcript_test_runs WHERE label = 'saving'), 'saved');
CREATE TEMP TABLE transcript_reapply_snapshot AS
SELECT r.*, s.id AS segment_id, s.created_at AS segment_created_at
FROM public.meeting_runs r JOIN public.transcript_segments s ON s.run_id = r.id
WHERE r.id = (SELECT id FROM transcript_test_runs WHERE label = 'saving');
RESET ROLE;
\i /migrations/0004_transcript_segments.sql
SET ROLE authenticated;
DO $$
DECLARE r public.meeting_runs;
BEGIN
  SELECT * INTO r FROM public.meeting_runs WHERE id = (SELECT id FROM transcript_test_runs WHERE label = 'saving');
  IF r.transcript_state <> 'saved' OR r.bytes_received <> 25 OR r.chunks_received <> 1
     OR (SELECT text FROM public.transcript_segments WHERE run_id = r.id) <> 'first final' THEN
    RAISE EXCEPTION 'migration reapply changed saved content/metadata';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM transcript_reapply_snapshot old
    JOIN public.transcript_segments s ON s.run_id = r.id
    WHERE old.id = r.id AND old.save_transcript = r.save_transcript
      AND old.started_at = r.started_at AND old.transcript_expires_at = r.transcript_expires_at
      AND old.segment_id = s.id AND old.segment_created_at = s.created_at) THEN
    RAISE EXCEPTION 'migration reapply replaced consent, expiry, or segment identity';
  END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state((SELECT id FROM transcript_test_runs WHERE label = 'disabled'), 'saved')) THEN
    RAISE EXCEPTION 'disabled run became saved';
  END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state(r.id, 'saving')) THEN
    RAISE EXCEPTION 'saved run resumed saving';
  END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state(r.id, 'bogus')) THEN
    RAISE EXCEPTION 'invalid storage state was accepted';
  END IF;
END $$;
SELECT * FROM public.append_transcript_final((SELECT id FROM transcript_test_runs WHERE label = 'failed'), 2, 'incomplete final', 0, 1, 'en');
DO $$
DECLARE failed_run uuid := (SELECT id FROM transcript_test_runs WHERE label = 'failed');
BEGIN
  IF (SELECT transcript_state FROM public.set_transcript_state(failed_run, 'failed')) IS DISTINCT FROM 'failed' THEN
    RAISE EXCEPTION 'saving run did not become incomplete';
  END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state(failed_run, 'saved')) THEN
    RAISE EXCEPTION 'failed archive was falsely marked complete';
  END IF;
  IF (SELECT text FROM public.transcript_segments s WHERE s.run_id = failed_run AND s.seq = 2) IS DISTINCT FROM 'incomplete final' THEN
    RAISE EXCEPTION 'incomplete final was not readable';
  END IF;
END $$;
-- A second account must not observe or change another account's archived finals.
SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$
DECLARE run_id uuid := (SELECT id FROM transcript_test_runs WHERE label = 'saving');
BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments) THEN RAISE EXCEPTION 'foreign text was visible'; END IF;
  IF EXISTS (SELECT 1 FROM public.append_transcript_final(run_id, 5, 'foreign append', 0, 1, 'en')) THEN
    RAISE EXCEPTION 'foreign append succeeded'; END IF;
  IF EXISTS (SELECT 1 FROM public.delete_transcript(run_id)) THEN RAISE EXCEPTION 'foreign delete succeeded'; END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state(run_id, 'failed')) THEN RAISE EXCEPTION 'foreign state change succeeded'; END IF;
  BEGIN
    INSERT INTO public.meeting_runs(user_id, meeting_id, save_transcript)
    SELECT auth.uid(), meeting_id, true FROM transcript_test_runs WHERE label = 'saving';
    RAISE EXCEPTION 'run linked to foreign meeting';
  EXCEPTION WHEN check_violation OR insufficient_privilege THEN NULL; END;
  BEGIN
    INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
    SELECT auth.uid(), meeting_id, id, 8, 'foreign direct insert', 0, 1, 'en'
    FROM transcript_test_runs WHERE label = 'saving';
    RAISE EXCEPTION 'foreign direct insert succeeded';
  EXCEPTION WHEN check_violation OR insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;
SET ROLE anon;
DO $$ BEGIN
  BEGIN
    PERFORM * FROM public.transcript_segments;
    RAISE EXCEPTION 'anonymous transcript read was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    PERFORM * FROM public.append_transcript_final(gen_random_uuid(), 1, 'anonymous', 0, 1, 'en');
    RAISE EXCEPTION 'anonymous append was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    PERFORM * FROM public.delete_transcript(gen_random_uuid());
    RAISE EXCEPTION 'anonymous delete was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    PERFORM * FROM public.set_transcript_state(gen_random_uuid(), 'saved');
    RAISE EXCEPTION 'anonymous state change was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
DO $$
DECLARE r public.meeting_runs; deleted_id uuid;
BEGIN
  SELECT * INTO r FROM public.delete_transcript((SELECT id FROM transcript_test_runs WHERE label = 'saving'));
  IF r.transcript_state <> 'deleted' OR NOT r.save_transcript OR r.bytes_received <> 25 OR r.chunks_received <> 1
     OR r.status <> 'active' OR r.transcript_expires_at <> r.started_at + interval '30 days' THEN
    RAISE EXCEPTION 'delete changed consent, retention, or capture metadata';
  END IF;
  deleted_id := r.id;
  IF (SELECT count(*) FROM public.delete_transcript(deleted_id)) <> 1 THEN RAISE EXCEPTION 'repeat deletion was not idempotent'; END IF;
  IF EXISTS (SELECT 1 FROM public.append_transcript_final(deleted_id, 9, 'revival', 0, 1, 'en')) THEN
    RAISE EXCEPTION 'deleted run accepted final'; END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state(deleted_id, 'saved')) THEN RAISE EXCEPTION 'deleted run revived via RPC'; END IF;
  BEGIN
    UPDATE public.meeting_runs SET transcript_state = 'saving' WHERE id = deleted_id;
    RAISE EXCEPTION 'deleted run revived via PATCH';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
    VALUES (auth.uid(), r.meeting_id, deleted_id, 9, 'direct revival', 0, 1, 'en');
    RAISE EXCEPTION 'deleted run accepted direct final';
  EXCEPTION WHEN object_not_in_prerequisite_state THEN NULL; END;
  SELECT * INTO r FROM public.delete_transcript((SELECT id FROM transcript_test_runs WHERE label = 'empty'));
  IF r.transcript_state <> 'deleted' THEN RAISE EXCEPTION 'empty archive was not terminally deleted'; END IF;
  SELECT * INTO r FROM public.delete_transcript((SELECT id FROM transcript_test_runs WHERE label = 'disabled'));
  IF r.transcript_state <> 'deleted' OR r.save_transcript THEN RAISE EXCEPTION 'disabled empty archive delete changed consent'; END IF;
  SELECT * INTO r FROM public.delete_transcript((SELECT id FROM transcript_test_runs WHERE label = 'failed'));
  IF r.transcript_state IS DISTINCT FROM 'deleted' THEN RAISE EXCEPTION 'failed archive did not delete'; END IF;
  IF EXISTS (SELECT 1 FROM public.delete_transcript(gen_random_uuid())) THEN RAISE EXCEPTION 'missing archive returned metadata'; END IF;
  BEGIN
    PERFORM public.purge_expired_transcripts();
    RAISE EXCEPTION 'application role could invoke admin purge';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id IN (SELECT id FROM transcript_test_runs)) THEN
    RAISE EXCEPTION 'deleted text was hidden rather than physically removed'; END IF;
END $$;
-- Admin fixtures use short real expiry; no disable-trigger or application bypass.
INSERT INTO public.meeting_runs(id, user_id, meeting_id, save_transcript, started_at)
SELECT 'a0000000-0000-4000-8000-000000000001', '11111111-2222-4333-8444-555555555555', id, true,
       clock_timestamp() - interval '30 days' + interval '1 second'
FROM public.meeting_sessions WHERE user_id = '11111111-2222-4333-8444-555555555555';
INSERT INTO public.meeting_runs(id, user_id, meeting_id, save_transcript, started_at)
SELECT 'a0000000-0000-4000-8000-000000000002', '11111111-2222-4333-8444-555555555555', id, true,
       clock_timestamp() - interval '30 days' + interval '1 second'
FROM public.meeting_sessions WHERE user_id = '11111111-2222-4333-8444-555555555555';
INSERT INTO public.meeting_runs(id, user_id, meeting_id, save_transcript)
SELECT 'a0000000-0000-4000-8000-000000000003', '11111111-2222-4333-8444-555555555555', id, true
FROM public.meeting_sessions WHERE user_id = '11111111-2222-4333-8444-555555555555';
SET ROLE authenticated;
SELECT * FROM public.append_transcript_final('a0000000-0000-4000-8000-000000000001', 1, 'expired purge final', 0, 1, 'en');
SELECT * FROM public.append_transcript_final('a0000000-0000-4000-8000-000000000002', 1, 'expired delete final', 0, 1, 'en');
SELECT * FROM public.append_transcript_final('a0000000-0000-4000-8000-000000000003', 7, 'unexpired final', 3, 4, 'id');
SELECT * FROM public.append_audio_chunk('a0000000-0000-4000-8000-000000000003', 19);
SELECT * FROM public.set_transcript_state('a0000000-0000-4000-8000-000000000003', 'saved');
SELECT pg_sleep(1.1);
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id IN ('a0000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000002')) THEN
    RAISE EXCEPTION 'expired text remained visible via RLS'; END IF;
  IF EXISTS (SELECT 1 FROM public.append_transcript_final('a0000000-0000-4000-8000-000000000001', 2, 'late final', 0, 1, 'en')) THEN
    RAISE EXCEPTION 'expired append succeeded'; END IF;
  IF EXISTS (SELECT 1 FROM public.set_transcript_state('a0000000-0000-4000-8000-000000000001', 'saved')) THEN
    RAISE EXCEPTION 'expired state was marked saved'; END IF;
END $$;
-- PATCH deleted must purge physically even when the owner SELECT policy hides expired text.
UPDATE public.meeting_runs SET transcript_state = 'deleted' WHERE id = 'a0000000-0000-4000-8000-000000000002';
RESET ROLE;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id = 'a0000000-0000-4000-8000-000000000002') THEN
    RAISE EXCEPTION 'direct PATCH deleted did not physically purge expired text'; END IF;
  IF public.purge_expired_transcripts() <> 1 THEN RAISE EXCEPTION 'admin purge did not remove exactly the expired final'; END IF;
  IF public.purge_expired_transcripts() <> 0 THEN RAISE EXCEPTION 'admin purge was not idempotent'; END IF;
  IF (SELECT count(*) FROM public.meeting_runs WHERE id IN ('a0000000-0000-4000-8000-000000000001', 'a0000000-0000-4000-8000-000000000002')) <> 2 THEN
    RAISE EXCEPTION 'purge removed run metadata'; END IF;
  IF NOT EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id = 'a0000000-0000-4000-8000-000000000003'
    AND seq = 7 AND text = 'unexpired final' AND start = 3 AND duration = 4 AND language = 'id') THEN
    RAISE EXCEPTION 'purge changed unexpired final'; END IF;
  IF NOT EXISTS (SELECT 1 FROM public.meeting_runs WHERE id = 'a0000000-0000-4000-8000-000000000003'
    AND transcript_state = 'saved' AND status = 'active' AND bytes_received = 19 AND chunks_received = 1) THEN
    RAISE EXCEPTION 'purge changed unexpired run/counters'; END IF;
END $$;
-- A direct PATCH on an active archive also purges, without deleting another archive.
SET ROLE authenticated;
UPDATE public.meeting_runs SET transcript_state = 'deleted' WHERE id = 'a0000000-0000-4000-8000-000000000003';
RESET ROLE;
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id = 'a0000000-0000-4000-8000-000000000003') THEN
    RAISE EXCEPTION 'active direct PATCH left physical text'; END IF;
END $$;
-- Administrative FK cascades must continue to work through the mutation guards.
INSERT INTO public.meeting_runs(id, user_id, meeting_id, save_transcript)
SELECT 'a0000000-0000-4000-8000-000000000004', '11111111-2222-4333-8444-555555555555', id, true
FROM public.meeting_sessions WHERE user_id = '11111111-2222-4333-8444-555555555555';
SET ROLE authenticated;
SELECT * FROM public.append_transcript_final('a0000000-0000-4000-8000-000000000004', 1, 'cascade final', 0, 1, 'en');
RESET ROLE;
DELETE FROM public.meeting_runs WHERE id = 'a0000000-0000-4000-8000-000000000004';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id = 'a0000000-0000-4000-8000-000000000004') THEN
    RAISE EXCEPTION 'parent FK cascade left a final'; END IF;
END $$;
INSERT INTO auth.users VALUES ('c0000000-0000-4000-8000-000000000001');
INSERT INTO public.meeting_sessions(id, user_id, provider, provider_host, provider_room_id)
VALUES ('c0000000-0000-4000-8000-000000000002', 'c0000000-0000-4000-8000-000000000001', 'jitsi', 'meet.jit.si', 'cascade-room');
INSERT INTO public.meeting_runs(id, user_id, meeting_id, save_transcript)
VALUES ('c0000000-0000-4000-8000-000000000003', 'c0000000-0000-4000-8000-000000000001', 'c0000000-0000-4000-8000-000000000002', true);
SET ROLE authenticated;
SET request.jwt.claim.sub = 'c0000000-0000-4000-8000-000000000001';
SELECT * FROM public.append_transcript_final('c0000000-0000-4000-8000-000000000003', 1, 'account cascade final', 0, 1, 'en');
RESET ROLE;
DELETE FROM auth.users WHERE id = 'c0000000-0000-4000-8000-000000000001';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.transcript_segments WHERE run_id = 'c0000000-0000-4000-8000-000000000003')
     OR EXISTS (SELECT 1 FROM public.meeting_runs WHERE id = 'c0000000-0000-4000-8000-000000000003')
     OR EXISTS (SELECT 1 FROM public.meeting_sessions WHERE id = 'c0000000-0000-4000-8000-000000000002') THEN
    RAISE EXCEPTION 'account FK cascade left transcript or metadata';
  END IF;
END $$;
UPDATE public.meeting_runs
SET status = 'stopped', ended_at = COALESCE(ended_at, now())
WHERE status = 'active';
CREATE UNIQUE INDEX meeting_runs_one_active_owner_idx
  ON public.meeting_runs(user_id)
  WHERE status = 'active';
SELECT 'transcript consent, immutable finals, RLS, deletion, reapply and retention checks passed' AS result;
