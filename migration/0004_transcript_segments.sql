-- Apply after 0003. Application RPCs use owner RLS, never elevated credentials.
-- 0005 installs the real scheduler separately; this schema needs no extensions.
BEGIN;
ALTER TABLE public.meeting_runs
  ADD COLUMN IF NOT EXISTS save_transcript boolean NOT NULL DEFAULT false,
  ADD COLUMN IF NOT EXISTS transcript_state text NOT NULL DEFAULT 'disabled',
  ADD COLUMN IF NOT EXISTS transcript_expires_at timestamptz;
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'public.meeting_runs'::regclass
    AND conname = 'meeting_runs_transcript_state_check') THEN
    ALTER TABLE public.meeting_runs ADD CONSTRAINT meeting_runs_transcript_state_check
      CHECK (transcript_state IN ('disabled', 'saving', 'saved', 'failed', 'deleted'));
  END IF;
  IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conrelid = 'public.meeting_runs'::regclass
    AND conname = 'meeting_runs_transcript_consent_check') THEN
    ALTER TABLE public.meeting_runs ADD CONSTRAINT meeting_runs_transcript_consent_check CHECK (
      (save_transcript AND transcript_expires_at = started_at + interval '30 days'
        AND transcript_expires_at IS NOT NULL AND transcript_state <> 'disabled') OR
      (NOT save_transcript AND transcript_expires_at IS NULL AND transcript_state IN ('disabled', 'deleted'))
    );
  END IF;
END $$;
CREATE TABLE IF NOT EXISTS public.transcript_segments (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  run_id uuid NOT NULL REFERENCES public.meeting_runs(id) ON DELETE CASCADE,
  seq integer NOT NULL CHECK (seq > 0),
  text text NOT NULL,
  start double precision NOT NULL CHECK (start >= 0 AND start < 'Infinity'::double precision),
  duration double precision NOT NULL CHECK (duration >= 0 AND duration < 'Infinity'::double precision),
  language text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT transcript_segments_run_seq_key UNIQUE (run_id, seq)
);
-- The unique index is also the ordered run/seq read index.
CREATE INDEX IF NOT EXISTS meeting_runs_transcript_expiry_idx
  ON public.meeting_runs(transcript_expires_at) WHERE save_transcript;
ALTER TABLE public.transcript_segments ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.transcript_segments FROM PUBLIC, anon, authenticated;
GRANT SELECT, DELETE ON public.transcript_segments TO authenticated;
GRANT INSERT (user_id, meeting_id, run_id, seq, text, start, duration, language)
  ON public.transcript_segments TO authenticated;
GRANT INSERT (save_transcript) ON public.meeting_runs TO authenticated;
GRANT UPDATE (transcript_state) ON public.meeting_runs TO authenticated;
DROP POLICY IF EXISTS transcript_segments_select_own ON public.transcript_segments;
CREATE POLICY transcript_segments_select_own ON public.transcript_segments
  FOR SELECT TO authenticated USING (
    user_id = (SELECT auth.uid()) AND EXISTS (
      SELECT 1 FROM public.meeting_runs r
      WHERE r.id = run_id AND r.user_id = transcript_segments.user_id
        AND r.meeting_id = transcript_segments.meeting_id AND r.save_transcript
        AND r.transcript_state IN ('saving', 'saved', 'failed')
        AND r.transcript_expires_at > clock_timestamp()
    )
  );
DROP POLICY IF EXISTS transcript_segments_insert_own ON public.transcript_segments;
CREATE POLICY transcript_segments_insert_own ON public.transcript_segments
  FOR INSERT TO authenticated WITH CHECK (
    user_id = (SELECT auth.uid()) AND EXISTS (
      SELECT 1 FROM public.meeting_runs r
      WHERE r.id = run_id AND r.user_id = transcript_segments.user_id
        AND r.meeting_id = transcript_segments.meeting_id AND r.save_transcript
        AND r.transcript_state IN ('saving', 'saved', 'failed')
        AND r.transcript_expires_at > clock_timestamp()
    )
  );
DROP POLICY IF EXISTS transcript_segments_delete_terminal_own ON public.transcript_segments;
CREATE POLICY transcript_segments_delete_terminal_own ON public.transcript_segments
  FOR DELETE TO authenticated USING (
    user_id = (SELECT auth.uid()) AND EXISTS (
      SELECT 1 FROM public.meeting_runs r WHERE r.id = run_id
        AND r.user_id = transcript_segments.user_id AND r.transcript_state = 'deleted'
    )
  );
-- No UPDATE grants or policies: a final never becomes an editable draft.
CREATE OR REPLACE FUNCTION public.livetranslate_guard_transcript_run()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NOT EXISTS (SELECT 1 FROM public.meeting_sessions m WHERE m.id = NEW.meeting_id AND m.user_id = NEW.user_id) THEN
      RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run parent ownership mismatch';
    END IF;
    NEW.transcript_state := CASE WHEN NEW.save_transcript THEN 'saving' ELSE 'disabled' END;
    NEW.transcript_expires_at := CASE WHEN NEW.save_transcript THEN NEW.started_at + interval '30 days' ELSE NULL END;
    RETURN NEW;
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.user_id IS DISTINCT FROM OLD.user_id
     OR NEW.meeting_id IS DISTINCT FROM OLD.meeting_id OR NEW.started_at IS DISTINCT FROM OLD.started_at
     OR NEW.save_transcript IS DISTINCT FROM OLD.save_transcript
     OR NEW.transcript_expires_at IS DISTINCT FROM OLD.transcript_expires_at THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run transcript consent and retention are immutable';
  END IF;
  IF NEW.transcript_state IS DISTINCT FROM OLD.transcript_state THEN
    IF NEW.transcript_state = 'deleted' THEN
      RETURN NEW;
    END IF;
    IF NOT OLD.save_transcript OR OLD.transcript_state IN ('deleted', 'disabled', 'failed')
       OR OLD.transcript_expires_at <= clock_timestamp()
       OR NEW.transcript_state NOT IN ('saved', 'failed')
       OR (OLD.transcript_state = 'saved' AND NEW.transcript_state <> 'failed') THEN
      RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'transcript state transition is not allowed';
    END IF;
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_transcript_run() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_transcript_run_guard ON public.meeting_runs;
CREATE TRIGGER livetranslate_transcript_run_guard BEFORE INSERT OR UPDATE ON public.meeting_runs
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_transcript_run();
CREATE OR REPLACE FUNCTION public.livetranslate_guard_transcript_segment()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE parent public.meeting_runs; database_admin boolean;
BEGIN
  IF TG_OP = 'DELETE' THEN
    -- Only the actual table owner (including inherited ownership) or a superuser
    -- may do maintenance/cascades directly. No JWT claim or caller-settable flag.
    SELECT role.rolsuper OR pg_catalog.pg_has_role(current_user, c.relowner, 'USAGE')
      INTO database_admin FROM pg_catalog.pg_class c
      JOIN pg_catalog.pg_roles role ON role.rolname = current_user
      WHERE c.oid = 'public.transcript_segments'::regclass;
    IF NOT database_admin AND pg_catalog.pg_trigger_depth() = 1 THEN
      RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'use delete_transcript to remove an archive';
    END IF;
    -- Nested application deletes still pass owner+terminal DELETE RLS. Trigger
    -- depth is only the direct-statement rejection, never a permission bypass.
    RETURN NULL;
  END IF;
  IF TG_OP = 'UPDATE' THEN
    RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'transcript finals are immutable';
  END IF;
  SELECT r.* INTO parent FROM public.meeting_runs r WHERE r.id = NEW.run_id FOR UPDATE;
  IF NOT FOUND OR NEW.user_id IS DISTINCT FROM parent.user_id
     OR NEW.meeting_id IS DISTINCT FROM parent.meeting_id
     OR NOT EXISTS (SELECT 1 FROM public.meeting_sessions m WHERE m.id = parent.meeting_id AND m.user_id = parent.user_id) THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'transcript parent ownership mismatch';
  END IF;
  -- Re-read wall clock only after acquiring the same parent lock used by delete.
  IF NOT parent.save_transcript OR parent.transcript_state IN ('disabled', 'deleted')
     OR parent.transcript_expires_at <= clock_timestamp() THEN
    RAISE EXCEPTION USING ERRCODE = '55000', MESSAGE = 'transcript storage is unavailable';
  END IF;
  IF NEW.seq IS NULL OR NEW.seq <= 0 OR NEW.text IS NULL OR NEW.language IS NULL
     OR NEW.start IS NULL OR NOT (NEW.start >= 0 AND NEW.start < 'Infinity'::double precision)
     OR NEW.duration IS NULL OR NOT (NEW.duration >= 0 AND NEW.duration < 'Infinity'::double precision) THEN
    -- Avoid native CHECK/NOT NULL details, which can include the private row text.
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'invalid transcript final';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_transcript_segment() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_transcript_segment_write_guard ON public.transcript_segments;
CREATE TRIGGER livetranslate_transcript_segment_write_guard BEFORE INSERT OR UPDATE ON public.transcript_segments
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_transcript_segment();
DROP TRIGGER IF EXISTS livetranslate_transcript_segment_delete_guard ON public.transcript_segments;
CREATE TRIGGER livetranslate_transcript_segment_delete_guard BEFORE DELETE ON public.transcript_segments
  FOR EACH STATEMENT EXECUTE FUNCTION public.livetranslate_guard_transcript_segment();
CREATE OR REPLACE FUNCTION public.livetranslate_purge_deleted_transcript()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE database_admin boolean;
BEGIN
  IF NEW.transcript_state <> 'deleted' THEN RETURN NULL; END IF;
  SELECT role.rolsuper OR pg_catalog.pg_has_role(current_user, c.relowner, 'USAGE')
    INTO database_admin FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_roles role ON role.rolname = current_user
    WHERE c.oid = 'public.transcript_segments'::regclass;
  IF database_admin THEN
    DELETE FROM public.transcript_segments WHERE run_id = NEW.id;
  ELSE
    -- No selected columns, WHERE, or RETURNING: PostgreSQL applies DELETE RLS,
    -- not the SELECT policy that intentionally hides deleted/expired text.
    -- DELETE RLS admits only this owner's terminal archives. Every earlier
    -- terminal transition was already purged in its own transaction.
    DELETE FROM public.transcript_segments;
  END IF;
  RETURN NULL;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_purge_deleted_transcript() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_transcript_run_delete ON public.meeting_runs;
CREATE TRIGGER livetranslate_transcript_run_delete AFTER UPDATE OF transcript_state ON public.meeting_runs
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_purge_deleted_transcript();
CREATE OR REPLACE FUNCTION public.append_transcript_final(
  run_uuid uuid, segment_seq integer, segment_text text, segment_start double precision,
  segment_duration double precision, segment_language text
) RETURNS SETOF public.transcript_segments
LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE parent public.meeting_runs; existing public.transcript_segments;
BEGIN
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  IF NOT EXISTS (SELECT 1 FROM public.meeting_sessions m WHERE m.id = parent.meeting_id AND m.user_id = parent.user_id) THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'transcript parent ownership mismatch';
  END IF;
  IF NOT parent.save_transcript OR parent.transcript_state IN ('disabled', 'deleted')
     OR parent.transcript_expires_at <= clock_timestamp() THEN RETURN; END IF;
  SELECT s.* INTO existing FROM public.transcript_segments s WHERE s.run_id = run_uuid AND s.seq = segment_seq;
  IF FOUND THEN
    IF existing.text IS NOT DISTINCT FROM segment_text AND existing.start IS NOT DISTINCT FROM segment_start
       AND existing.duration IS NOT DISTINCT FROM segment_duration AND existing.language IS NOT DISTINCT FROM segment_language THEN
      RETURN NEXT existing;
      RETURN;
    END IF;
    RAISE EXCEPTION USING ERRCODE = '23505', MESSAGE = 'transcript final conflicts with an existing sequence';
  END IF;
  BEGIN
    RETURN QUERY INSERT INTO public.transcript_segments(user_id, meeting_id, run_id, seq, text, start, duration, language)
      VALUES (parent.user_id, parent.meeting_id, parent.id, segment_seq, segment_text, segment_start, segment_duration, segment_language)
      RETURNING *;
  EXCEPTION WHEN object_not_in_prerequisite_state THEN
    -- Expiry can advance between the RPC check and the insertion guard.
    RETURN;
  WHEN insufficient_privilege THEN
    -- INSERT RLS evaluates the clock again after the insertion trigger.
    IF parent.transcript_expires_at <= clock_timestamp() THEN RETURN; END IF;
    RAISE;
  END;
END $$;
CREATE OR REPLACE FUNCTION public.delete_transcript(run_uuid uuid)
RETURNS SETOF public.meeting_runs LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE parent public.meeting_runs;
BEGIN
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  -- The UPDATE trigger couples all exposed state PATCH paths to physical purge.
  RETURN QUERY UPDATE public.meeting_runs r SET transcript_state = 'deleted'
    WHERE r.id = parent.id RETURNING r.*;
END $$;
CREATE OR REPLACE FUNCTION public.set_transcript_state(run_uuid uuid, state_value text)
RETURNS SETOF public.meeting_runs LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE parent public.meeting_runs;
BEGIN
  SELECT r.* INTO parent FROM public.meeting_runs r
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  IF state_value IS NULL OR state_value NOT IN ('saved', 'failed') OR NOT parent.save_transcript
     OR parent.transcript_state IN ('disabled', 'deleted', 'failed')
     OR parent.transcript_expires_at <= clock_timestamp()
     OR (parent.transcript_state = 'saved' AND state_value <> 'failed') THEN RETURN; END IF;
  BEGIN
    RETURN QUERY UPDATE public.meeting_runs r SET transcript_state = state_value WHERE r.id = parent.id RETURNING r.*;
  EXCEPTION WHEN check_violation THEN
    IF parent.transcript_expires_at <= clock_timestamp() THEN RETURN; END IF;
    RAISE;
  END;
END $$;
CREATE OR REPLACE FUNCTION public.purge_expired_transcripts()
RETURNS bigint LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE removed bigint; database_admin boolean;
BEGIN
  SELECT role.rolsuper OR pg_catalog.pg_has_role(current_user, c.relowner, 'USAGE')
    INTO database_admin FROM pg_catalog.pg_class c
    JOIN pg_catalog.pg_roles role ON role.rolname = current_user
    WHERE c.oid = 'public.transcript_segments'::regclass;
  IF NOT database_admin THEN
    RAISE EXCEPTION USING ERRCODE = '42501', MESSAGE = 'transcript retention requires database administration';
  END IF;
  DELETE FROM public.transcript_segments s USING public.meeting_runs r
    WHERE r.id = s.run_id AND r.transcript_expires_at <= clock_timestamp();
  GET DIAGNOSTICS removed = ROW_COUNT;
  RETURN removed;
END $$;
REVOKE ALL ON FUNCTION public.append_transcript_final(uuid, integer, text, double precision, double precision, text) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION public.delete_transcript(uuid) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION public.set_transcript_state(uuid, text) FROM PUBLIC, anon, authenticated;
REVOKE ALL ON FUNCTION public.purge_expired_transcripts() FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.append_transcript_final(uuid, integer, text, double precision, double precision, text) TO authenticated;
GRANT EXECUTE ON FUNCTION public.delete_transcript(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.set_transcript_state(uuid, text) TO authenticated;
NOTIFY pgrst, 'reload schema';
COMMIT;
