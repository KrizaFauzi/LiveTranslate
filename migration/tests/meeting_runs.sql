-- Local isolated PostgreSQL harness for 0003: run accounting, RLS, and function security.
-- Runs after meeting_sessions.sql, which creates the roles, auth schema, and meeting rows.
\i /migrations/0003_meeting_runs.sql
\i /migrations/0003_meeting_runs.sql
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
CREATE TEMP TABLE own_session AS SELECT id FROM public.meeting_sessions;
INSERT INTO public.meeting_runs (user_id, meeting_id) SELECT auth.uid(), id FROM own_session;
DO $$ BEGIN
  IF (SELECT count(*) FROM public.meeting_runs) <> 1 THEN RAISE EXCEPTION 'run not created'; END IF;
  IF (SELECT user_id FROM public.meeting_runs) <> auth.uid() THEN RAISE EXCEPTION 'run owner mismatch'; END IF;
END $$;
SELECT * FROM public.append_audio_chunk((SELECT id FROM public.meeting_runs), 100);
SELECT * FROM public.append_audio_chunk((SELECT id FROM public.meeting_runs), 50);
DO $$ BEGIN
  IF (SELECT bytes_received FROM public.meeting_runs) <> 150 THEN RAISE EXCEPTION 'byte accounting wrong'; END IF;
  IF (SELECT chunks_received FROM public.meeting_runs) <> 2 THEN RAISE EXCEPTION 'chunk accounting wrong'; END IF;
END $$;
SELECT * FROM public.stop_audio_run((SELECT id FROM public.meeting_runs));
DO $$ BEGIN
  IF (SELECT count(*) FROM public.stop_audio_run((SELECT id FROM public.meeting_runs))) <> 0 THEN RAISE EXCEPTION 'stop changed an already stopped run'; END IF;
  IF (SELECT status FROM public.meeting_runs) <> 'stopped' THEN RAISE EXCEPTION 'run not stopped'; END IF;
  IF (SELECT ended_at FROM public.meeting_runs) IS NULL THEN RAISE EXCEPTION 'ended_at missing'; END IF;
  IF (SELECT count(*) FROM public.append_audio_chunk((SELECT id FROM public.meeting_runs), 10)) <> 0 THEN RAISE EXCEPTION 'stopped run accepted a chunk'; END IF;
  IF (SELECT bytes_received FROM public.meeting_runs) <> 150 THEN RAISE EXCEPTION 'bytes changed after stop'; END IF;
END $$;
SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF (SELECT count(*) FROM public.meeting_runs) <> 0 THEN RAISE EXCEPTION 'foreign run visible'; END IF;
  IF (SELECT count(*) FROM public.append_audio_chunk((SELECT id FROM public.meeting_runs), 10)) <> 0 THEN RAISE EXCEPTION 'foreign chunk accepted'; END IF;
  IF (SELECT count(*) FROM public.stop_audio_run((SELECT id FROM public.meeting_runs))) <> 0 THEN RAISE EXCEPTION 'foreign stop accepted'; END IF;
END $$;
RESET ROLE;
SET ROLE anon;
DO $$ BEGIN
  BEGIN
    PERFORM * FROM public.meeting_runs;
    RAISE EXCEPTION 'anonymous read was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    PERFORM * FROM public.append_audio_chunk(gen_random_uuid(), 10);
    RAISE EXCEPTION 'anonymous function call was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;
SELECT 'meeting run accounting and RLS checks passed' AS result;
