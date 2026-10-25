-- P0-3 regression: source linkage, deterministic upsert and owner isolation.
\i /migrations/0007_translations.sql
\i /migrations/0007_translations.sql

SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
CREATE TEMP TABLE translation_occurrence AS
  SELECT * FROM public.connect_meeting_occurrence((SELECT id FROM public.meeting_sessions WHERE user_id = auth.uid() LIMIT 1));
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM translation_occurrence));
CREATE TEMP TABLE translation_run AS
  WITH inserted AS (
    INSERT INTO public.meeting_runs(user_id, meeting_id, occurrence_id, save_transcript, transcript_state, transcript_expires_at)
    VALUES (auth.uid(), (SELECT meeting_id FROM translation_occurrence), (SELECT id FROM translation_occurrence), true, 'saving', now() + interval '24 hours') RETURNING *
  ) SELECT * FROM inserted;
SELECT * FROM public.append_transcript_final((SELECT id FROM translation_run), 1, 'How are you?', 0, 1, 'en');
SELECT * FROM public.put_translation((SELECT id FROM translation_run), 1, 'Apa kabar?', 'success', NULL, 1);
SELECT * FROM public.put_translation((SELECT id FROM translation_run), 1, 'Apa kabar?', 'success', NULL, 1);
SELECT * FROM public.append_transcript_final((SELECT id FROM translation_run), 2, 'This queue overflowed.', 1, 1, 'en');
SELECT public.fail_missing_translations((SELECT id FROM translation_run),'translation_queue_overflow');

DO $$ BEGIN
  IF (SELECT count(*) FROM public.translations WHERE run_id = (SELECT id FROM translation_run)) <> 2 THEN
    RAISE EXCEPTION 'translation upsert/reconciliation count is incorrect';
  END IF;
  IF (SELECT text FROM public.translations WHERE run_id = (SELECT id FROM translation_run) AND source_seq=1) <> 'Apa kabar?' THEN
    RAISE EXCEPTION 'translation text mismatch';
  END IF;
  IF NOT EXISTS (
    SELECT 1 FROM public.translations WHERE run_id=(SELECT id FROM translation_run)
      AND source_seq=2 AND status='failed' AND error_code='translation_queue_overflow'
  ) THEN RAISE EXCEPTION 'missing translation was not terminally reconciled'; END IF;
  IF NOT EXISTS (
    SELECT 1 FROM public.translations t JOIN public.transcript_segments s ON s.id = t.source_segment_id
    WHERE t.run_id = (SELECT id FROM translation_run) AND t.source_seq = s.seq AND t.occurrence_id = s.occurrence_id
  ) THEN RAISE EXCEPTION 'translation source linkage mismatch'; END IF;
END $$;

SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.translations WHERE run_id = (SELECT id FROM translation_run)) THEN
    RAISE EXCEPTION 'foreign translation visible';
  END IF;
  IF (SELECT count(*) FROM public.put_translation((SELECT id FROM translation_run), 1, 'Bocor', 'success', NULL, 1)) <> 0 THEN
    RAISE EXCEPTION 'foreign translation write allowed';
  END IF;
END $$;
RESET ROLE;
UPDATE public.meeting_runs
SET status = 'stopped', ended_at = COALESCE(ended_at, now())
WHERE id = (SELECT id FROM translation_run);

SELECT 'translation linkage and RLS checks passed' AS result;
