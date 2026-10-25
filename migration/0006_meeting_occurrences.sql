-- Apply after 0004. Adds per-meeting occurrence identity and post-meeting decision lifecycle.
-- Historical runs are mapped one-to-one because room/date metadata cannot prove that
-- separate captures belonged to the same actual meeting.
BEGIN;

CREATE TABLE IF NOT EXISTS public.meeting_occurrences (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL DEFAULT auth.uid() REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'pending_documents' CHECK (
    status IN ('pending_documents', 'active', 'awaiting_decision', 'discarding', 'archived', 'discarded')
  ),
  started_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz,
  staging_expires_at timestamptz,
  consent_given_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT meeting_occurrences_time_check CHECK (ended_at IS NULL OR ended_at >= started_at),
  CONSTRAINT meeting_occurrences_lifecycle_fields_check CHECK (
    (status IN ('pending_documents', 'active') AND staging_expires_at IS NULL AND consent_given_at IS NULL)
    OR (status IN ('awaiting_decision', 'discarding') AND ended_at IS NOT NULL AND staging_expires_at IS NOT NULL AND consent_given_at IS NULL)
    OR (status = 'archived' AND ended_at IS NOT NULL AND staging_expires_at IS NULL AND consent_given_at IS NOT NULL)
    OR (status = 'discarded' AND ended_at IS NOT NULL AND staging_expires_at IS NULL AND consent_given_at IS NULL)
  )
);

CREATE INDEX IF NOT EXISTS meeting_occurrences_user_started_idx
  ON public.meeting_occurrences(user_id, started_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS meeting_occurrences_staging_expiry_idx
  ON public.meeting_occurrences(staging_expires_at)
  WHERE status = 'awaiting_decision';

ALTER TABLE public.meeting_occurrences ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.meeting_occurrences FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.meeting_occurrences TO authenticated;
GRANT INSERT (user_id, meeting_id) ON public.meeting_occurrences TO authenticated;
REVOKE UPDATE ON public.meeting_occurrences FROM authenticated;

DROP POLICY IF EXISTS meeting_occurrences_select_own ON public.meeting_occurrences;
CREATE POLICY meeting_occurrences_select_own ON public.meeting_occurrences
  FOR SELECT TO authenticated USING (user_id = (SELECT auth.uid()));
DROP POLICY IF EXISTS meeting_occurrences_insert_own ON public.meeting_occurrences;
CREATE POLICY meeting_occurrences_insert_own ON public.meeting_occurrences
  FOR INSERT TO authenticated WITH CHECK (
    user_id = (SELECT auth.uid()) AND EXISTS (
      SELECT 1 FROM public.meeting_sessions m
      WHERE m.id = meeting_id AND m.user_id = meeting_occurrences.user_id
    )
  );
DROP POLICY IF EXISTS meeting_occurrences_update_own ON public.meeting_occurrences;
CREATE POLICY meeting_occurrences_update_own ON public.meeting_occurrences
  FOR UPDATE TO authenticated USING (user_id = (SELECT auth.uid()))
  WITH CHECK (user_id = (SELECT auth.uid()));

ALTER TABLE public.meeting_runs ADD COLUMN IF NOT EXISTS occurrence_id uuid;

DO $$
DECLARE historical public.meeting_runs;
DECLARE occurrence_uuid uuid;
BEGIN
  FOR historical IN
    SELECT * FROM public.meeting_runs WHERE occurrence_id IS NULL ORDER BY started_at, id
  LOOP
    occurrence_uuid := gen_random_uuid();
    INSERT INTO public.meeting_occurrences(
      id, user_id, meeting_id, status, started_at, ended_at,
      staging_expires_at, consent_given_at
    ) VALUES (
      occurrence_uuid,
      historical.user_id,
      historical.meeting_id,
      CASE
        WHEN historical.status = 'active' THEN 'active'
        WHEN historical.save_transcript THEN 'archived'
        ELSE 'discarded'
      END,
      historical.started_at,
      COALESCE(historical.ended_at, historical.started_at),
      NULL,
      CASE WHEN historical.status <> 'active' AND historical.save_transcript
        THEN historical.started_at ELSE NULL END
    );
    UPDATE public.meeting_runs SET occurrence_id = occurrence_uuid WHERE id = historical.id;
  END LOOP;
END $$;

ALTER TABLE public.meeting_runs
  ALTER COLUMN occurrence_id SET NOT NULL;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'public.meeting_runs'::regclass
      AND conname = 'meeting_runs_occurrence_id_fkey'
  ) THEN
    ALTER TABLE public.meeting_runs ADD CONSTRAINT meeting_runs_occurrence_id_fkey
      FOREIGN KEY (occurrence_id) REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE;
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS meeting_runs_occurrence_idx ON public.meeting_runs(occurrence_id, started_at, id);
GRANT INSERT (occurrence_id, save_transcript, transcript_state, transcript_expires_at) ON public.meeting_runs TO authenticated;

ALTER TABLE public.transcript_segments ADD COLUMN IF NOT EXISTS occurrence_id uuid;
UPDATE public.transcript_segments s
  SET occurrence_id = r.occurrence_id
  FROM public.meeting_runs r
  WHERE r.id = s.run_id AND s.occurrence_id IS NULL;
ALTER TABLE public.transcript_segments ALTER COLUMN occurrence_id SET NOT NULL;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conrelid = 'public.transcript_segments'::regclass
      AND conname = 'transcript_segments_occurrence_id_fkey'
  ) THEN
    ALTER TABLE public.transcript_segments ADD CONSTRAINT transcript_segments_occurrence_id_fkey
      FOREIGN KEY (occurrence_id) REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE;
  END IF;
END $$;
GRANT INSERT (occurrence_id) ON public.transcript_segments TO authenticated;

CREATE OR REPLACE FUNCTION public.livetranslate_guard_occurrence()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    IF NEW.user_id IS DISTINCT FROM (SELECT auth.uid())
      OR NOT EXISTS (
        SELECT 1 FROM public.meeting_sessions m
        WHERE m.id = NEW.meeting_id AND m.user_id = NEW.user_id
      ) THEN
      RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'occurrence parent ownership mismatch';
    END IF;
    NEW.status := 'pending_documents';
    NEW.started_at := clock_timestamp();
    NEW.ended_at := NULL;
    NEW.staging_expires_at := NULL;
    NEW.consent_given_at := NULL;
    RETURN NEW;
  END IF;
  IF NEW.id IS DISTINCT FROM OLD.id OR NEW.user_id IS DISTINCT FROM OLD.user_id
    OR NEW.meeting_id IS DISTINCT FROM OLD.meeting_id OR NEW.started_at IS DISTINCT FROM OLD.started_at
    OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'occurrence identity is immutable';
  END IF;
  IF NEW.status IS NOT DISTINCT FROM OLD.status THEN RETURN NEW; END IF;
  IF NOT (
    (OLD.status = 'pending_documents' AND NEW.status IN ('active', 'discarding'))
    OR (OLD.status = 'active' AND NEW.status IN ('awaiting_decision', 'discarding'))
    OR (OLD.status = 'awaiting_decision' AND NEW.status IN ('archived', 'discarding'))
    OR (OLD.status = 'discarding' AND NEW.status = 'discarded')
  ) THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'occurrence state transition is not allowed';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_occurrence() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_occurrence_guard ON public.meeting_occurrences;
CREATE TRIGGER livetranslate_occurrence_guard
  BEFORE INSERT OR UPDATE ON public.meeting_occurrences
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_occurrence();

CREATE OR REPLACE FUNCTION public.livetranslate_guard_run_occurrence()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.meeting_occurrences o
    WHERE o.id = NEW.occurrence_id AND o.user_id = NEW.user_id
      AND o.meeting_id = NEW.meeting_id AND o.status = 'active'
  ) THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run occurrence is not active or owned';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.occurrence_id IS DISTINCT FROM OLD.occurrence_id THEN
    RAISE EXCEPTION USING ERRCODE = '23514', MESSAGE = 'run occurrence is immutable';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_run_occurrence() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_run_occurrence_guard ON public.meeting_runs;
CREATE TRIGGER livetranslate_run_occurrence_guard
  BEFORE INSERT OR UPDATE OF occurrence_id ON public.meeting_runs
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_run_occurrence();

CREATE OR REPLACE FUNCTION public.connect_meeting_occurrence(meeting_uuid uuid)
RETURNS SETOF public.meeting_occurrences
LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.meeting_sessions m
    WHERE m.id = meeting_uuid AND m.user_id = (SELECT auth.uid())
  ) THEN RETURN; END IF;
  RETURN QUERY INSERT INTO public.meeting_occurrences(user_id, meeting_id)
    VALUES ((SELECT auth.uid()), meeting_uuid)
    RETURNING *;
END $$;

CREATE OR REPLACE FUNCTION public.activate_meeting_occurrence(occurrence_uuid uuid)
RETURNS SETOF public.meeting_occurrences
LANGUAGE sql SECURITY DEFINER SET search_path = '' AS $$
  UPDATE public.meeting_occurrences o SET status = 'active'
    WHERE o.id = occurrence_uuid AND o.user_id = (SELECT auth.uid())
      AND o.status = 'pending_documents'
  RETURNING o.*;
$$;

CREATE OR REPLACE FUNCTION public.finish_meeting_occurrence(occurrence_uuid uuid)
RETURNS SETOF public.meeting_occurrences
LANGUAGE sql SECURITY DEFINER SET search_path = '' AS $$
  UPDATE public.meeting_occurrences o
    SET status = 'awaiting_decision', ended_at = clock_timestamp(),
        staging_expires_at = clock_timestamp() + interval '24 hours'
    WHERE o.id = occurrence_uuid AND o.user_id = (SELECT auth.uid())
      AND o.status = 'active'
  RETURNING o.*;
$$;

CREATE OR REPLACE FUNCTION public.append_transcript_final(
  run_uuid uuid, segment_seq integer, segment_text text, segment_start double precision,
  segment_duration double precision, segment_language text
) RETURNS SETOF public.transcript_segments
LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
DECLARE parent public.meeting_runs; existing public.transcript_segments;
BEGIN
  SELECT r.* INTO parent FROM public.meeting_runs r
    JOIN public.meeting_occurrences o ON o.id = r.occurrence_id
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid())
      AND o.user_id = r.user_id AND o.meeting_id = r.meeting_id
      AND o.status IN ('active', 'awaiting_decision')
    FOR UPDATE OF r;
  IF NOT FOUND OR NOT parent.save_transcript OR parent.transcript_state IN ('disabled', 'deleted')
    OR parent.transcript_expires_at <= clock_timestamp() THEN RETURN; END IF;
  SELECT s.* INTO existing FROM public.transcript_segments s
    WHERE s.run_id = run_uuid AND s.seq = segment_seq;
  IF FOUND THEN
    IF existing.text IS NOT DISTINCT FROM segment_text
      AND existing.start IS NOT DISTINCT FROM segment_start
      AND existing.duration IS NOT DISTINCT FROM segment_duration
      AND existing.language IS NOT DISTINCT FROM segment_language
      AND existing.occurrence_id IS NOT DISTINCT FROM parent.occurrence_id THEN
      RETURN NEXT existing;
      RETURN;
    END IF;
    RAISE EXCEPTION USING ERRCODE = '23505',
      MESSAGE = 'transcript final conflicts with an existing sequence';
  END IF;
  BEGIN
    RETURN QUERY INSERT INTO public.transcript_segments(
      user_id, meeting_id, occurrence_id, run_id, seq, text, start, duration, language
    ) VALUES (
      parent.user_id, parent.meeting_id, parent.occurrence_id, parent.id,
      segment_seq, segment_text, segment_start, segment_duration, segment_language
    ) RETURNING *;
  EXCEPTION WHEN object_not_in_prerequisite_state THEN
    RETURN;
  WHEN insufficient_privilege THEN
    IF parent.transcript_expires_at <= clock_timestamp() THEN RETURN; END IF;
    RAISE;
  END;
END $$;

CREATE OR REPLACE FUNCTION public.livetranslate_guard_segment_occurrence()
RETURNS trigger LANGUAGE plpgsql SECURITY INVOKER SET search_path = '' AS $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM public.meeting_runs r
    JOIN public.meeting_occurrences o ON o.id = r.occurrence_id
    WHERE r.id = NEW.run_id AND r.user_id = NEW.user_id
      AND r.meeting_id = NEW.meeting_id
      AND r.occurrence_id = NEW.occurrence_id
      AND o.user_id = NEW.user_id AND o.meeting_id = NEW.meeting_id
      AND o.status IN ('active', 'awaiting_decision')
  ) THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      MESSAGE = 'transcript occurrence ownership mismatch';
  END IF;
  IF TG_OP = 'UPDATE' AND NEW.occurrence_id IS DISTINCT FROM OLD.occurrence_id THEN
    RAISE EXCEPTION USING ERRCODE = '23514',
      MESSAGE = 'transcript occurrence is immutable';
  END IF;
  RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION public.livetranslate_guard_segment_occurrence() FROM PUBLIC, anon, authenticated;
DROP TRIGGER IF EXISTS livetranslate_segment_occurrence_guard ON public.transcript_segments;
CREATE TRIGGER livetranslate_segment_occurrence_guard
  BEFORE INSERT OR UPDATE OF occurrence_id ON public.transcript_segments
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_guard_segment_occurrence();
REVOKE ALL ON FUNCTION public.append_transcript_final(uuid, integer, text, double precision, double precision, text)
  FROM PUBLIC, anon, authenticated;
GRANT EXECUTE ON FUNCTION public.append_transcript_final(uuid, integer, text, double precision, double precision, text)
  TO authenticated;

REVOKE ALL ON FUNCTION public.connect_meeting_occurrence(uuid) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.activate_meeting_occurrence(uuid) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.finish_meeting_occurrence(uuid) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.connect_meeting_occurrence(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.activate_meeting_occurrence(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.finish_meeting_occurrence(uuid) TO authenticated;

NOTIFY pgrst, 'reload schema';
COMMIT;
