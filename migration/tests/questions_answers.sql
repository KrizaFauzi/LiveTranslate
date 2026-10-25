-- P0-5 regression: stable question identity, evidence retrieval, abstention invariant, owner isolation.
\i /migrations/0009_questions_answers.sql
\i /migrations/0009_questions_answers.sql
SET ROLE authenticated; SET request.jwt.claim.sub='11111111-2222-4333-8444-555555555555';
CREATE TEMP TABLE qa_occurrence AS SELECT * FROM public.connect_meeting_occurrence((SELECT id FROM public.meeting_sessions WHERE user_id=auth.uid() LIMIT 1));
CREATE TEMP TABLE qa_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM qa_occurrence),'brief.csv','text/csv',20,repeat('d',64));
SELECT * FROM public.commit_document_extraction((SELECT id FROM qa_document),'[{"locator":{"kind":"csv_row","row":2},"text":"project deadline Friday"}]','{}');
SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM qa_occurrence));
CREATE TEMP TABLE qa_run AS WITH x AS(INSERT INTO public.meeting_runs(user_id,meeting_id,occurrence_id,save_transcript,transcript_state,transcript_expires_at) VALUES(auth.uid(),(SELECT meeting_id FROM qa_occurrence),(SELECT id FROM qa_occurrence),true,'saving',now()+interval '24 hours') RETURNING *)SELECT * FROM x;
SELECT * FROM public.append_transcript_final((SELECT id FROM qa_run),1,'What is the project deadline?',0,1,'en');
CREATE TEMP TABLE qa_question AS SELECT * FROM public.create_question((SELECT id FROM qa_run),1,'What is the project deadline?',repeat('e',64));
SELECT * FROM public.put_answer(
 (SELECT id FROM qa_question),'answered','Batas waktu proyek adalah Jumat.','The project deadline is Friday.',
 jsonb_build_array(jsonb_build_object('document_id',(SELECT id FROM qa_document),'file_name','brief.csv','document_version',1,'chunk_index',0,'locator',jsonb_build_object('kind','csv_row','row',2),'verbatim_text','project deadline Friday','supports_id',true,'supports_en',true)),0
);
SELECT * FROM public.complete_assistance_segment((SELECT id FROM qa_run),1,'completed',NULL);
SELECT * FROM public.append_transcript_final((SELECT id FROM qa_run),2,'This segment overflowed assistance.',1,1,'en');
SELECT public.fail_missing_assistance_segments((SELECT id FROM qa_run),'assistance_queue_overflow');
CREATE TEMP TABLE qa_failed_document AS SELECT * FROM public.create_meeting_document((SELECT id FROM qa_occurrence),'broken.pdf','application/pdf',10,repeat('f',64));
SELECT * FROM public.fail_document_extraction((SELECT id FROM qa_failed_document),'pdf_invalid_or_encrypted');
CREATE TEMP TABLE qa_failed_question AS SELECT * FROM public.create_question((SELECT id FROM qa_run),2,'What was in the broken document?',repeat('a',64));
SELECT * FROM public.put_answer((SELECT id FROM qa_failed_question),'parse_failed','-','-','[]',0);
DO $$ BEGIN
 IF (SELECT count(*) FROM public.create_question((SELECT id FROM qa_run),1,'What is the project deadline?',repeat('e',64)))<>1 THEN RAISE EXCEPTION 'question dedupe failed'; END IF;
 IF (SELECT count(*) FROM public.questions WHERE id=(SELECT id FROM qa_question))<>1 THEN RAISE EXCEPTION 'duplicate question created'; END IF;
 IF (SELECT count(*) FROM public.search_document_chunks((SELECT id FROM qa_occurrence),'project deadline',5))<>1 THEN RAISE EXCEPTION 'evidence retrieval failed'; END IF;
 IF (SELECT count(*) FROM public.put_answer((SELECT id FROM qa_question),'answered','invented','invented','[]',0))<>0 THEN RAISE EXCEPTION 'answer without citation accepted';END IF;
 IF (SELECT count(*) FROM public.put_answer((SELECT id FROM qa_question),'answered','invented','invented',jsonb_build_array(jsonb_build_object('document_id','00000000-0000-4000-8000-000000000001','file_name','brief.csv','document_version',1,'chunk_index',0,'locator',jsonb_build_object('kind','csv_row','row',2))),0))<>0 THEN RAISE EXCEPTION 'invented citation accepted';END IF;
 IF (SELECT count(*) FROM public.put_answer((SELECT id FROM qa_question),'answered','invented','invented',jsonb_build_array(jsonb_build_object('document_id',(SELECT id FROM qa_document),'file_name','brief.csv','document_version',1,'chunk_index',0,'locator',jsonb_build_object('kind','csv_row','row',2),'verbatim_text','not present in source','supports_id',true,'supports_en',true)),0))<>0 THEN RAISE EXCEPTION 'invented evidence quote accepted';END IF;
 IF NOT EXISTS(SELECT 1 FROM public.assistance_segment_status WHERE run_id=(SELECT id FROM qa_run) AND source_seq=2 AND status='failed' AND error_code='assistance_queue_overflow') THEN RAISE EXCEPTION 'missing assistance status was not terminally reconciled';END IF;
 IF public.classify_document_evidence_state((SELECT id FROM qa_occurrence))<>'parse_failed' THEN RAISE EXCEPTION 'failed document was reported as no evidence';END IF;
 IF NOT EXISTS(SELECT 1 FROM public.answers WHERE question_id=(SELECT id FROM qa_failed_question) AND status='parse_failed') THEN RAISE EXCEPTION 'parse_failed answer state was rejected';END IF;
END $$;
SET request.jwt.claim.sub='66666666-2222-4333-8444-555555555555';
DO $$ BEGIN IF EXISTS(SELECT 1 FROM public.questions WHERE id=(SELECT id FROM qa_question)) OR EXISTS(SELECT 1 FROM public.answers WHERE question_id=(SELECT id FROM qa_question)) THEN RAISE EXCEPTION 'foreign QA visible';END IF;END $$;
RESET ROLE;
UPDATE public.meeting_runs
SET status = 'stopped', ended_at = COALESCE(ended_at, now())
WHERE id = (SELECT id FROM qa_run);
SELECT 'question evidence and RLS checks passed' AS result;
