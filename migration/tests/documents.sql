-- P0-4 regression: limits, versions, extraction locators, and owner isolation.
\i /migrations/0008_documents.sql
\i /migrations/0008_documents.sql
SET ROLE authenticated;
SET request.jwt.claim.sub='11111111-2222-4333-8444-555555555555';
CREATE TEMP TABLE document_occurrence AS SELECT * FROM public.connect_meeting_occurrence((SELECT id FROM public.meeting_sessions WHERE user_id=auth.uid() LIMIT 1));
CREATE TEMP TABLE created_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM document_occurrence),'brief.csv','text/csv',12,repeat('a',64));
SELECT * FROM public.commit_document_extraction((SELECT id FROM created_document),'[{"locator":{"kind":"csv_row","row":1},"text":"name | value"}]'::jsonb,ARRAY[]::text[]);
DO $$ BEGIN
 IF (SELECT status FROM public.documents WHERE id=(SELECT id FROM created_document))<>'ready' THEN RAISE EXCEPTION 'document not ready'; END IF;
 IF (SELECT locator->>'kind' FROM public.document_chunks WHERE document_id=(SELECT id FROM created_document))<>'csv_row' THEN RAISE EXCEPTION 'locator lost'; END IF;
 IF (SELECT version FROM public.create_meeting_document((SELECT id FROM document_occurrence),'brief.csv','text/csv',12,repeat('b',64)))<>2 THEN RAISE EXCEPTION 'version not incremented'; END IF;
END $$;
SET request.jwt.claim.sub='66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
 IF EXISTS(SELECT 1 FROM public.documents WHERE id=(SELECT id FROM created_document)) THEN RAISE EXCEPTION 'foreign document visible'; END IF;
 IF EXISTS(SELECT 1 FROM public.document_chunks WHERE document_id=(SELECT id FROM created_document)) THEN RAISE EXCEPTION 'foreign chunk visible'; END IF;
 IF (SELECT count(*) FROM public.create_meeting_document((SELECT id FROM document_occurrence),'stolen.csv','text/csv',12,repeat('c',64)))<>0 THEN RAISE EXCEPTION 'foreign document create allowed'; END IF;
END $$;
RESET ROLE;
SELECT 'document lifecycle and RLS checks passed' AS result;
