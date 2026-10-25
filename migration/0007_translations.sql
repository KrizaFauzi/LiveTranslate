-- Apply after 0006. Stores Indonesian translations linked to immutable final transcript segments.
BEGIN;

CREATE TABLE IF NOT EXISTS public.translations (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL DEFAULT auth.uid() REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
  run_id uuid NOT NULL REFERENCES public.meeting_runs(id) ON DELETE CASCADE,
  source_segment_id uuid NOT NULL REFERENCES public.transcript_segments(id) ON DELETE CASCADE,
  source_seq integer NOT NULL CHECK (source_seq > 0),
  source_revision integer NOT NULL DEFAULT 1 CHECK (source_revision > 0),
  language text NOT NULL DEFAULT 'id' CHECK (language = 'id'),
  text text,
  status text NOT NULL CHECK (status IN ('success', 'failed')),
  error_code text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT translations_result_check CHECK (
    (status = 'success' AND text IS NOT NULL AND length(btrim(text)) > 0 AND error_code IS NULL)
    OR (status = 'failed' AND text IS NULL AND error_code IS NOT NULL AND length(btrim(error_code)) > 0)
  ),
  UNIQUE (source_segment_id, source_revision),
  UNIQUE (run_id, source_seq, source_revision)
);

CREATE INDEX IF NOT EXISTS translations_occurrence_source_idx
  ON public.translations(occurrence_id, run_id, source_seq, source_revision DESC);

ALTER TABLE public.translations ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.translations FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.translations TO authenticated;

DROP POLICY IF EXISTS translations_select_own ON public.translations;
CREATE POLICY translations_select_own ON public.translations
  FOR SELECT TO authenticated USING (user_id = (SELECT auth.uid()));

CREATE OR REPLACE FUNCTION public.put_translation(
  run_uuid uuid, segment_seq integer, translated_text text,
  translation_status text, translation_error_code text DEFAULT NULL,
  translation_revision integer DEFAULT 1
) RETURNS SETOF public.translations
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE parent public.meeting_runs; source public.transcript_segments;
BEGIN
  IF translation_status NOT IN ('success', 'failed') OR translation_revision < 1 THEN
    RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid translation result';
  END IF;
  SELECT r.* INTO parent FROM public.meeting_runs r
    JOIN public.meeting_occurrences o ON o.id = r.occurrence_id
    WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid())
      AND o.user_id = r.user_id AND o.status IN ('active', 'awaiting_decision')
    FOR UPDATE OF r;
  IF NOT FOUND THEN RETURN; END IF;
  SELECT s.* INTO source FROM public.transcript_segments s
    WHERE s.run_id = parent.id AND s.seq = segment_seq AND s.user_id = parent.user_id;
  IF NOT FOUND THEN RETURN; END IF;
  RETURN QUERY INSERT INTO public.translations(
    user_id, meeting_id, occurrence_id, run_id, source_segment_id, source_seq,
    source_revision, language, text, status, error_code
  ) VALUES (
    parent.user_id, parent.meeting_id, parent.occurrence_id, parent.id, source.id, source.seq,
    translation_revision, 'id', CASE WHEN translation_status = 'success' THEN btrim(translated_text) ELSE NULL END,
    translation_status, CASE WHEN translation_status = 'failed' THEN btrim(translation_error_code) ELSE NULL END
  )
  ON CONFLICT (source_segment_id, source_revision) DO UPDATE SET
    text = EXCLUDED.text, status = EXCLUDED.status, error_code = EXCLUDED.error_code,
    updated_at = clock_timestamp()
  WHERE public.translations.user_id = (SELECT auth.uid())
  RETURNING public.translations.*;
END $$;

CREATE OR REPLACE FUNCTION public.fail_missing_translations(run_uuid uuid,failure_code text DEFAULT 'translation_queue_overflow')
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE parent public.meeting_runs; affected integer;
BEGIN
  IF btrim(failure_code)='' THEN RAISE EXCEPTION USING ERRCODE='22023',MESSAGE='invalid translation failure'; END IF;
  SELECT r.* INTO parent FROM public.meeting_runs r
    JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
    WHERE r.id=run_uuid AND r.user_id=(SELECT auth.uid())
      AND o.user_id=r.user_id AND o.status IN('active','awaiting_decision')
    FOR UPDATE OF r;
  IF NOT FOUND THEN RETURN 0; END IF;
  INSERT INTO public.translations(user_id,meeting_id,occurrence_id,run_id,source_segment_id,source_seq,source_revision,language,status,error_code)
    SELECT s.user_id,s.meeting_id,s.occurrence_id,s.run_id,s.id,s.seq,1,'id','failed',btrim(failure_code)
    FROM public.transcript_segments s
    WHERE s.run_id=parent.id AND s.user_id=parent.user_id
      AND NOT EXISTS(SELECT 1 FROM public.translations t WHERE t.source_segment_id=s.id AND t.source_revision=1)
    ON CONFLICT(source_segment_id,source_revision) DO NOTHING;
  GET DIAGNOSTICS affected=ROW_COUNT;
  RETURN affected;
END $$;

REVOKE ALL ON FUNCTION public.fail_missing_translations(uuid,text) FROM PUBLIC,anon;
GRANT EXECUTE ON FUNCTION public.fail_missing_translations(uuid,text) TO authenticated;

REVOKE ALL ON FUNCTION public.put_translation(uuid, integer, text, text, text, integer) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.put_translation(uuid, integer, text, text, text, integer) TO authenticated;

NOTIFY pgrst, 'reload schema';
COMMIT;
