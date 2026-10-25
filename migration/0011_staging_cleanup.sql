-- Apply after 0010 as database administrator. Archives have no age-based purge.
BEGIN;

-- Cut occurrence-bound transcripts over from the historical 30-day policy.
-- Staging retention is owned by meeting_occurrences; committed archives do not expire.
DROP INDEX IF EXISTS public.meeting_runs_transcript_expiry_idx;
ALTER TABLE public.meeting_runs DROP CONSTRAINT IF EXISTS meeting_runs_transcript_consent_check;
DROP TRIGGER IF EXISTS livetranslate_transcript_run_guard ON public.meeting_runs;
UPDATE public.meeting_runs SET transcript_expires_at = NULL WHERE occurrence_id IS NOT NULL;
CREATE OR REPLACE FUNCTION public.livetranslate_guard_transcript_run()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM public.meeting_sessions m WHERE m.id = NEW.meeting_id AND m.user_id = NEW.user_id) THEN
      RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run parent ownership mismatch';
    END IF;
    NEW.transcript_state := CASE WHEN NEW.save_transcript THEN 'saving' ELSE 'disabled' END;
    NEW.transcript_expires_at := NULL;
    RETURN NEW;
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.user_id IS DISTINCT FROM OLD.user_id
     OR NEW.meeting_id IS DISTINCT FROM OLD.meeting_id OR NEW.started_at IS DISTINCT FROM OLD.started_at
     OR NEW.save_transcript IS DISTINCT FROM OLD.save_transcript
     OR NEW.transcript_expires_at IS DISTINCT FROM OLD.transcript_expires_at THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run transcript consent is immutable';
  END IF;
  IF NEW.transcript_state IS DISTINCT FROM OLD.transcript_state THEN
    IF NEW.transcript_state = 'deleted' THEN RETURN NEW; END IF;
    IF NOT OLD.save_transcript OR OLD.transcript_state IN ('deleted', 'disabled', 'failed')
       OR NEW.transcript_state NOT IN ('saved', 'failed')
       OR (OLD.transcript_state = 'saved' AND NEW.transcript_state <> 'failed') THEN
      RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'transcript state transition is not allowed';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_transcript_run() FROM PUBLIC, anon, authenticated;
CREATE TRIGGER livetranslate_transcript_run_guard BEFORE INSERT OR UPDATE ON public.meeting_runs
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_transcript_run();
ALTER TABLE public.meeting_runs ADD CONSTRAINT meeting_runs_transcript_consent_check CHECK (
  (save_transcript AND transcript_expires_at IS NULL AND transcript_state <> 'disabled')
  OR (NOT save_transcript AND transcript_expires_at IS NULL AND transcript_state IN ('disabled','deleted'))
);

DROP POLICY IF EXISTS transcript_segments_select_own ON public.transcript_segments;
CREATE POLICY transcript_segments_select_own ON public.transcript_segments
  FOR SELECT TO authenticated USING (
    user_id=(SELECT auth.uid()) AND EXISTS(
      SELECT 1 FROM public.meeting_runs r
      JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
      WHERE r.id=run_id AND r.user_id=transcript_segments.user_id
        AND r.meeting_id=transcript_segments.meeting_id
        AND r.save_transcript AND r.transcript_state IN('saving','saved','failed')
        AND o.user_id=r.user_id AND o.status IN('active','awaiting_decision','archived')
    )
  );
DROP POLICY IF EXISTS transcript_segments_insert_own ON public.transcript_segments;
CREATE POLICY transcript_segments_insert_own ON public.transcript_segments
  FOR INSERT TO authenticated WITH CHECK (
    user_id=(SELECT auth.uid()) AND EXISTS(
      SELECT 1 FROM public.meeting_runs r
      JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
      WHERE r.id=run_id AND r.user_id=transcript_segments.user_id
        AND r.meeting_id=transcript_segments.meeting_id
        AND r.save_transcript AND r.transcript_state IN('saving','saved','failed')
        AND o.user_id=r.user_id AND o.status IN('active','awaiting_decision')
    )
  );

CREATE OR REPLACE FUNCTION public.delete_transcript(run_uuid uuid)
RETURNS SETOF public.meeting_runs LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE parent public.meeting_runs;
DECLARE occurrence_status text;
BEGIN
  -- Read the immutable occurrence ID first, then take locks in the same
  -- occurrence-before-run order used by archive commit/delete.
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid());
  IF NOT FOUND THEN RETURN; END IF;
  IF parent.occurrence_id IS NOT NULL THEN
    SELECT o.status INTO occurrence_status FROM public.meeting_occurrences o
      WHERE o.id = parent.occurrence_id AND o.user_id = parent.user_id FOR UPDATE;
    IF NOT FOUND OR occurrence_status = 'archived' THEN RETURN; END IF;
  END IF;
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  RETURN QUERY UPDATE public.meeting_runs r SET transcript_state = 'deleted'
    WHERE r.id = parent.id RETURNING r.*;
END $$;
REVOKE ALL ON FUNCTION public.delete_transcript(uuid) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.delete_transcript(uuid) TO authenticated;

CREATE OR REPLACE FUNCTION public.set_transcript_state(run_uuid uuid,state_value text)
RETURNS SETOF public.meeting_runs LANGUAGE plpgsql SECURITY INVOKER SET search_path='' AS $$
DECLARE parent public.meeting_runs;
BEGIN
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id=run_uuid AND r.user_id=(SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND OR state_value NOT IN('saved','failed') OR NOT parent.save_transcript
     OR parent.transcript_state IN('disabled','deleted','failed')
     OR (parent.transcript_state='saved' AND state_value<>'failed') THEN RETURN; END IF;
  RETURN QUERY UPDATE public.meeting_runs r SET transcript_state=state_value
    WHERE r.id=parent.id RETURNING r.*;
END $$;

CREATE OR REPLACE FUNCTION public.purge_expired_occurrence_staging()
RETURNS bigint LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE database_admin boolean;
DECLARE occurrence public.meeting_occurrences;
DECLARE removed bigint := 0;
BEGIN
  SELECT role.rolsuper OR pg_catalog.pg_has_role(current_user, c.relowner, 'USAGE')
    INTO database_admin FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_roles role ON role.rolname = current_user
    WHERE c.oid = 'public.meeting_occurrences'::regclass;
  IF NOT database_admin THEN
    RAISE EXCEPTION USING ERRCODE = '42501',
      MESSAGE = 'staging retention requires database administration';
  END IF;
  FOR occurrence IN
    SELECT o.* FROM public.meeting_occurrences o
      WHERE o.status = 'awaiting_decision'
        AND o.staging_expires_at <= clock_timestamp()
      ORDER BY o.staging_expires_at, o.id
      FOR UPDATE SKIP LOCKED
  LOOP
    UPDATE public.meeting_occurrences o SET status = 'discarding'
      WHERE o.id = occurrence.id;
    INSERT INTO public.storage_cleanup_jobs(user_id,occurrence_id,object_path,reason)
      SELECT d.user_id,d.occurrence_id,d.object_path,'expiry' FROM public.documents d
      WHERE d.occurrence_id=occurrence.id
      ON CONFLICT(bucket_id,object_path) DO NOTHING;
    UPDATE public.meeting_runs r SET transcript_state = 'deleted'
      WHERE r.occurrence_id = occurrence.id
        AND r.save_transcript AND r.transcript_state <> 'deleted';
    DELETE FROM public.documents d WHERE d.occurrence_id=occurrence.id;
    UPDATE public.meeting_occurrences o
      SET status = 'discarded', staging_expires_at = NULL, consent_given_at = NULL
      WHERE o.id = occurrence.id;
    removed := removed + 1;
  END LOOP;
  RETURN removed;
END $$;

REVOKE ALL ON FUNCTION public.purge_expired_occurrence_staging() FROM PUBLIC, anon, authenticated;

-- Remove only the historical transcript-age schedule. The historical function
-- remains for rollback, but new occurrence archives are never selected by age.
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
    PERFORM cron.unschedule(jobid) FROM cron.job
      WHERE jobname = 'livetranslate-transcript-retention';
    IF NOT EXISTS (SELECT 1 FROM cron.job WHERE jobname = 'livetranslate-staging-retention') THEN
      PERFORM cron.schedule(
        'livetranslate-staging-retention',
        '*/15 * * * *',
        'SELECT public.purge_expired_occurrence_staging()'
      );
    END IF;
  END IF;
END $$;

NOTIFY pgrst, 'reload schema';
COMMIT;
