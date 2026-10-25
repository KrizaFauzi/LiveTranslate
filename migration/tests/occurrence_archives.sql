-- P0-1 regression: post-meeting save/discard and permanent archive semantics.
\i /migrations/0010_occurrence_archives.sql
\i /migrations/0010_occurrence_archives.sql
\i /migrations/0011_staging_cleanup.sql
\i /migrations/0011_staging_cleanup.sql

SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';

CREATE TEMP TABLE archive_candidate AS
SELECT * FROM public.connect_meeting_occurrence((SELECT id FROM public.meeting_sessions WHERE user_id=auth.uid() LIMIT 1));
CREATE TEMP TABLE archive_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM archive_candidate),'archive.csv','text/csv',12,repeat('f',64));
SELECT * FROM public.commit_document_extraction((SELECT id FROM archive_document),'[{"locator":{"kind":"csv_row","row":1},"text":"archive evidence"}]'::jsonb,ARRAY[]::text[]);
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM archive_candidate));
CREATE TEMP TABLE archive_run AS WITH x AS(
  INSERT INTO public.meeting_runs(user_id,meeting_id,occurrence_id,save_transcript,transcript_state,transcript_expires_at)
  VALUES(auth.uid(),(SELECT meeting_id FROM archive_candidate),(SELECT id FROM archive_candidate),true,'saving',now()+interval '24 hours') RETURNING *
) SELECT * FROM x;
SELECT * FROM public.append_transcript_final((SELECT id FROM archive_run),1,'Archive this sentence',0,1,'en');
SELECT * FROM public.finish_meeting_occurrence((SELECT id FROM archive_candidate));
DO $$ BEGIN
  IF (SELECT count(*) FROM public.commit_meeting_archive((SELECT id FROM archive_candidate))) <> 0 THEN
    RAISE EXCEPTION 'incomplete derived artifacts were archived';
  END IF;
END $$;
SELECT * FROM public.put_translation((SELECT id FROM archive_run),1,'Arsipkan kalimat ini','success',NULL,1);
SELECT * FROM public.complete_assistance_segment((SELECT id FROM archive_run),1,'completed',NULL);
DO $$ BEGIN
  IF (SELECT count(*) FROM public.commit_meeting_archive((SELECT id FROM archive_candidate))) <> 0 THEN
    RAISE EXCEPTION 'active or undrained run was archived';
  END IF;
END $$;
SELECT * FROM public.stop_audio_run((SELECT id FROM archive_run));
SELECT * FROM public.set_transcript_state((SELECT id FROM archive_run),'saved');

CREATE TEMP TABLE committed_archive AS
SELECT * FROM public.commit_meeting_archive((SELECT id FROM archive_candidate));

DO $$ BEGIN
  IF (SELECT count(*) FROM committed_archive) <> 1 THEN RAISE EXCEPTION 'archive commit returned no row'; END IF;
  IF (SELECT status FROM public.meeting_occurrences WHERE id = (SELECT occurrence_id FROM committed_archive)) <> 'archived' THEN
    RAISE EXCEPTION 'occurrence not archived';
  END IF;
  IF (SELECT consent_given_at FROM public.meeting_occurrences WHERE id = (SELECT occurrence_id FROM committed_archive)) IS NULL THEN
    RAISE EXCEPTION 'archive consent timestamp missing';
  END IF;
  IF (SELECT transcript_expires_at FROM public.meeting_runs WHERE id = (SELECT id FROM archive_run)) IS NOT NULL THEN
    RAISE EXCEPTION 'permanent archive retained transcript expiry';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM public.transcript_segments WHERE run_id = (SELECT id FROM archive_run) AND seq=1) THEN
    RAISE EXCEPTION 'permanent archive transcript is not readable';
  END IF;
  IF (SELECT staging_expires_at FROM public.meeting_occurrences WHERE id = (SELECT occurrence_id FROM committed_archive)) IS NOT NULL THEN
    RAISE EXCEPTION 'archived occurrence retained staging expiry';
  END IF;
  IF (SELECT count(*) FROM public.commit_meeting_archive((SELECT occurrence_id FROM committed_archive))) <> 1 THEN
    RAISE EXCEPTION 'archive commit is not idempotent';
  END IF;
  IF (SELECT count(*) FROM public.archives WHERE occurrence_id = (SELECT occurrence_id FROM committed_archive)) <> 1 THEN
    RAISE EXCEPTION 'archive commit duplicated manifest';
  END IF;
  IF (SELECT count(*) FROM public.discard_meeting_occurrence((SELECT occurrence_id FROM committed_archive))) <> 0 THEN
    RAISE EXCEPTION 'discard defeated committed archive';
  END IF;
  IF NOT public.verify_meeting_archive((SELECT id FROM committed_archive)) THEN
    RAISE EXCEPTION 'fresh archive manifest failed integrity verification';
  END IF;
END $$;
DO $$ BEGIN
  IF (SELECT count(*) FROM public.delete_transcript((SELECT id FROM archive_run))) <> 0 THEN
    RAISE EXCEPTION 'per-run delete bypassed committed archive deletion';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM public.transcript_segments WHERE run_id = (SELECT id FROM archive_run) AND seq=1)
     OR NOT public.verify_meeting_archive((SELECT id FROM committed_archive)) THEN
    RAISE EXCEPTION 'blocked per-run delete damaged committed archive';
  END IF;
END $$;

RESET ROLE;
UPDATE public.translations SET text='tampered' WHERE occurrence_id=(SELECT occurrence_id FROM committed_archive);
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF public.verify_meeting_archive((SELECT id FROM committed_archive)) THEN
    RAISE EXCEPTION 'tampered archive passed integrity verification';
  END IF;
END $$;
RESET ROLE;
UPDATE public.translations SET text='Arsipkan kalimat ini' WHERE occurrence_id=(SELECT occurrence_id FROM committed_archive);
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';

CREATE TEMP TABLE partial_candidate AS SELECT * FROM public.connect_meeting_occurrence((SELECT meeting_id FROM archive_candidate));
CREATE TEMP TABLE failed_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM partial_candidate),'failed.pdf','application/pdf',12,repeat('c',64));
SELECT * FROM public.fail_document_extraction((SELECT id FROM failed_document),'pdf_invalid_or_encrypted');
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM partial_candidate));
SELECT * FROM public.finish_meeting_occurrence((SELECT id FROM partial_candidate));
CREATE TEMP TABLE partial_archive AS SELECT * FROM public.commit_meeting_archive((SELECT id FROM partial_candidate));
DO $$ BEGIN
  IF (SELECT status FROM partial_archive) <> 'partial_failed' THEN
    RAISE EXCEPTION 'failed document did not produce an explicit partial archive';
  END IF;
END $$;

CREATE TEMP TABLE discard_candidate AS
SELECT * FROM public.connect_meeting_occurrence((SELECT meeting_id FROM archive_candidate));
CREATE TEMP TABLE discard_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM discard_candidate),'discard.csv','text/csv',12,repeat('a',64));
SELECT * FROM public.commit_document_extraction((SELECT id FROM discard_document),'[{"locator":{"kind":"csv_row","row":1},"text":"discard evidence"}]'::jsonb,ARRAY[]::text[]);
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM discard_candidate));
SELECT * FROM public.finish_meeting_occurrence((SELECT id FROM discard_candidate));
SELECT * FROM public.discard_meeting_occurrence((SELECT id FROM discard_candidate));

DO $$ BEGIN
  IF (SELECT status FROM public.meeting_occurrences WHERE id = (SELECT id FROM discard_candidate)) <> 'discarded' THEN
    RAISE EXCEPTION 'occurrence was not discarded';
  END IF;
  IF EXISTS (SELECT 1 FROM public.archives WHERE occurrence_id = (SELECT id FROM discard_candidate)) THEN
    RAISE EXCEPTION 'discard created an archive';
  END IF;
  IF EXISTS (SELECT 1 FROM public.documents WHERE id = (SELECT id FROM discard_document)) THEN
    RAISE EXCEPTION 'discard retained document metadata';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM public.storage_cleanup_jobs WHERE object_path = (SELECT object_path FROM discard_document) AND reason='discard' AND status='pending') THEN
    RAISE EXCEPTION 'discard did not queue physical object cleanup';
  END IF;
END $$;

CREATE TEMP TABLE expiry_candidate AS SELECT * FROM public.connect_meeting_occurrence((SELECT meeting_id FROM archive_candidate));
CREATE TEMP TABLE expiry_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM expiry_candidate),'expiry.csv','text/csv',12,repeat('b',64));
SELECT * FROM public.commit_document_extraction((SELECT id FROM expiry_document),'[{"locator":{"kind":"csv_row","row":1},"text":"expiry evidence"}]'::jsonb,ARRAY[]::text[]);
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM expiry_candidate));
SELECT * FROM public.finish_meeting_occurrence((SELECT id FROM expiry_candidate));

-- Expiry only discards awaiting decisions; archives are permanent by age.
RESET ROLE;
UPDATE public.meeting_occurrences
SET staging_expires_at = clock_timestamp() - interval '1 second'
WHERE id = (SELECT id FROM expiry_candidate);
RESET ROLE;
SELECT public.purge_expired_occurrence_staging();
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF (SELECT status FROM public.meeting_occurrences WHERE id = (SELECT occurrence_id FROM committed_archive)) <> 'archived' THEN
    RAISE EXCEPTION 'expiry changed permanent archive';
  END IF;
  IF EXISTS (SELECT 1 FROM public.documents WHERE id=(SELECT id FROM expiry_document)) THEN
    RAISE EXCEPTION 'expiry retained document metadata';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM public.storage_cleanup_jobs WHERE object_path=(SELECT object_path FROM expiry_document) AND reason='expiry') THEN
    RAISE EXCEPTION 'expiry did not queue physical object cleanup';
  END IF;
END $$;

SELECT public.delete_meeting_archive((SELECT id FROM committed_archive));
DO $$ BEGIN
  IF (SELECT status FROM public.archives WHERE id=(SELECT id FROM committed_archive)) <> 'deleted' THEN
    RAISE EXCEPTION 'archive delete did not retain a soft-delete marker';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM public.meeting_occurrences WHERE id=(SELECT occurrence_id FROM committed_archive)) THEN
    RAISE EXCEPTION 'archive delete removed recovery identity';
  END IF;
  IF EXISTS (SELECT 1 FROM public.documents WHERE id=(SELECT id FROM archive_document)) THEN
    RAISE EXCEPTION 'archive delete retained document metadata';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM public.storage_cleanup_jobs WHERE object_path=(SELECT object_path FROM archive_document) AND reason='archive_delete') THEN
    RAISE EXCEPTION 'archive delete did not queue physical object cleanup';
  END IF;
  IF NOT public.delete_meeting_archive((SELECT id FROM committed_archive)) THEN
    RAISE EXCEPTION 'archive delete is not idempotent';
  END IF;
END $$;

SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.archives a JOIN committed_archive c ON a.id = c.id) THEN
    RAISE EXCEPTION 'foreign archive visible';
  END IF;
END $$;
RESET ROLE;

SELECT 'occurrence archive commit, discard, expiry, and RLS checks passed' AS result;
