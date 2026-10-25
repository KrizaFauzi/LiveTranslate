-- Operational migration: apply as the database administrator after 0004.
-- Requires a PostgreSQL runtime with pg_cron installed/preloaded (Supabase Cron).
-- Deliberately excluded from the no-extension schema harness. Never a no-op fallback.
BEGIN;
DO $$
DECLARE retention_job bigint;
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_available_extensions WHERE name = 'pg_cron') THEN
    RAISE EXCEPTION USING ERRCODE = '55000',
      MESSAGE = 'transcript retention requires pg_cron; enable Supabase Cron before applying 0005';
  END IF;
  BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_cron;
  EXCEPTION WHEN OTHERS THEN
    RAISE EXCEPTION 'transcript retention could not activate pg_cron: %', SQLERRM
      USING ERRCODE = '55000', HINT = 'Enable/preload pg_cron in this database, then reapply 0005.';
  END;
  IF current_setting('cron.launch_active_jobs', true) = 'off' THEN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'transcript retention requires cron.launch_active_jobs = on';
  END IF;
  -- Named jobs are unique per installer. A deliberate administrator change must
  -- not retain the old installer's job as a second physical-retention scheduler.
  PERFORM cron.unschedule(j.jobid) FROM cron.job j
    WHERE j.jobname = 'livetranslate-transcript-retention' AND j.username <> current_user;
  SELECT cron.schedule('livetranslate-transcript-retention', '* * * * *',
    'SELECT public.purge_expired_transcripts();') INTO retention_job;
  -- schedule() upserts command/schedule but does not reactivate a paused job.
  UPDATE cron.job SET active = true WHERE jobid = retention_job;
END $$;
COMMIT;
