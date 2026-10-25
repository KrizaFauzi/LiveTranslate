-- Apply after 0008. Stable meeting questions and evidence-only bilingual answers.
BEGIN;
CREATE TABLE IF NOT EXISTS public.assistance_segment_status(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
 meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
 occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
 run_id uuid NOT NULL REFERENCES public.meeting_runs(id) ON DELETE CASCADE,
 source_segment_id uuid NOT NULL UNIQUE REFERENCES public.transcript_segments(id) ON DELETE CASCADE,
 source_seq integer NOT NULL CHECK(source_seq>0),
 status text NOT NULL CHECK(status IN('completed','failed')), error_code text,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT assistance_status_error_check CHECK((status='completed' AND error_code IS NULL) OR (status='failed' AND error_code IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS public.questions(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
 meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
 occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
 run_id uuid NOT NULL REFERENCES public.meeting_runs(id) ON DELETE CASCADE,
 source_segment_id uuid NOT NULL REFERENCES public.transcript_segments(id) ON DELETE CASCADE,
 source_seq integer NOT NULL CHECK(source_seq>0), text text NOT NULL CHECK(length(btrim(text))>0),
 normalized_hash text NOT NULL CHECK(normalized_hash ~ '^[0-9a-f]{64}$'), created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(occurrence_id,source_segment_id,normalized_hash)
);
CREATE TABLE IF NOT EXISTS public.answers(
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(), user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
 meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
 occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
 question_id uuid NOT NULL UNIQUE REFERENCES public.questions(id) ON DELETE CASCADE,
 answer_id text NOT NULL, answer_en text NOT NULL,
 status text NOT NULL CHECK(status IN('answered','no_evidence','retrieval_failed','provider_failed')),
 citations jsonb NOT NULL DEFAULT '[]'::jsonb, revision_count integer NOT NULL DEFAULT 0 CHECK(revision_count BETWEEN 0 AND 1),
 created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now(),
 CONSTRAINT answer_evidence_check CHECK(
  (status='answered' AND answer_id<>'-' AND answer_en<>'-' AND jsonb_array_length(citations)>0)
  OR (status<>'answered' AND answer_id='-' AND answer_en='-' AND jsonb_array_length(citations)=0)
 )
);
ALTER TABLE public.answers DROP CONSTRAINT IF EXISTS answers_status_check;
ALTER TABLE public.answers ADD CONSTRAINT answers_status_check
  CHECK(status IN('answered','no_evidence','retrieval_failed','provider_failed','parse_failed','quota_exceeded'));
CREATE INDEX IF NOT EXISTS questions_occurrence_idx ON public.questions(occurrence_id,created_at,id);
ALTER TABLE public.assistance_segment_status ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.assistance_segment_status FROM PUBLIC,anon,authenticated;
GRANT SELECT ON public.assistance_segment_status TO authenticated;
DROP POLICY IF EXISTS assistance_segment_status_select_own ON public.assistance_segment_status;
CREATE POLICY assistance_segment_status_select_own ON public.assistance_segment_status FOR SELECT TO authenticated USING(user_id=(SELECT auth.uid()));

ALTER TABLE public.questions ENABLE ROW LEVEL SECURITY; ALTER TABLE public.answers ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.questions,public.answers FROM PUBLIC,anon,authenticated; GRANT SELECT ON public.questions,public.answers TO authenticated;
DROP POLICY IF EXISTS questions_select_own ON public.questions; CREATE POLICY questions_select_own ON public.questions FOR SELECT TO authenticated USING(user_id=(SELECT auth.uid()));
DROP POLICY IF EXISTS answers_select_own ON public.answers; CREATE POLICY answers_select_own ON public.answers FOR SELECT TO authenticated USING(user_id=(SELECT auth.uid()));

CREATE OR REPLACE FUNCTION public.create_question(run_uuid uuid,segment_seq integer,question_text text,question_hash text)
RETURNS SETOF public.questions LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE parent public.meeting_runs; source public.transcript_segments;
BEGIN
 SELECT r.* INTO parent FROM public.meeting_runs r JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
  WHERE r.id=run_uuid AND r.user_id=(SELECT auth.uid()) AND o.status IN('active','awaiting_decision') FOR UPDATE OF r;
 IF NOT FOUND THEN RETURN; END IF;
 SELECT s.* INTO source FROM public.transcript_segments s WHERE s.run_id=parent.id AND s.seq=segment_seq AND s.user_id=parent.user_id;
 IF NOT FOUND OR btrim(question_text)='' OR question_hash !~ '^[0-9a-f]{64}$' THEN RETURN; END IF;
 RETURN QUERY INSERT INTO public.questions(user_id,meeting_id,occurrence_id,run_id,source_segment_id,source_seq,text,normalized_hash)
 VALUES(parent.user_id,parent.meeting_id,parent.occurrence_id,parent.id,source.id,source.seq,btrim(question_text),question_hash)
 ON CONFLICT(occurrence_id,source_segment_id,normalized_hash) DO UPDATE SET text=EXCLUDED.text RETURNING public.questions.*;
END $$;

CREATE OR REPLACE FUNCTION public.search_document_chunks(occurrence_uuid uuid,search_text text,result_limit integer DEFAULT 5)
RETURNS TABLE(document_id uuid,file_name text,document_version integer,chunk_index integer,locator jsonb,chunk_text text,rank real)
LANGUAGE sql SECURITY DEFINER STABLE SET search_path='' AS $$
 SELECT c.document_id,d.file_name,d.version,c.chunk_index,c.locator,c.text,
  ts_rank_cd(c.search_vector,plainto_tsquery('simple',search_text)) rank
 FROM public.document_chunks c JOIN public.documents d ON d.id=c.document_id
 WHERE c.occurrence_id=occurrence_uuid AND c.user_id=(SELECT auth.uid()) AND d.user_id=c.user_id AND d.status='ready'
  AND c.search_vector @@ plainto_tsquery('simple',search_text)
 ORDER BY rank DESC,c.document_id,c.chunk_index LIMIT LEAST(GREATEST(result_limit,1),10);
$$;

CREATE OR REPLACE FUNCTION public.classify_document_evidence_state(occurrence_uuid uuid)
RETURNS text LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path='' AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM public.meeting_occurrences o WHERE o.id=occurrence_uuid AND o.user_id=(SELECT auth.uid())) THEN
  RETURN 'retrieval_failed';
 END IF;
 IF EXISTS(SELECT 1 FROM public.documents d WHERE d.occurrence_id=occurrence_uuid AND d.user_id=(SELECT auth.uid()) AND d.status='failed') THEN
  RETURN 'parse_failed';
 END IF;
 IF EXISTS(SELECT 1 FROM public.documents d WHERE d.occurrence_id=occurrence_uuid AND d.user_id=(SELECT auth.uid()) AND d.status NOT IN('ready','failed')) THEN
  RETURN 'retrieval_failed';
 END IF;
 RETURN 'no_evidence';
END $$;

CREATE OR REPLACE FUNCTION public.put_answer(question_uuid uuid,result_status text,result_id text,result_en text,result_citations jsonb,result_revision_count integer DEFAULT 0)
RETURNS SETOF public.answers LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE parent public.questions;
BEGIN
 SELECT q.* INTO parent FROM public.questions q JOIN public.meeting_occurrences o ON o.id=q.occurrence_id
 WHERE q.id=question_uuid AND q.user_id=(SELECT auth.uid()) AND o.status IN('active','awaiting_decision') FOR UPDATE OF q;
 IF NOT FOUND THEN RETURN; END IF;
 IF result_status='answered' THEN
  IF jsonb_typeof(result_citations)<>'array' OR result_citations='[]'::jsonb OR btrim(result_id) IN('','-') OR btrim(result_en) IN('','-') THEN RETURN; END IF;
  IF EXISTS(
    SELECT 1 FROM jsonb_array_elements(result_citations) c
    WHERE NOT EXISTS(
      SELECT 1 FROM public.documents d JOIN public.document_chunks dc ON dc.document_id=d.id
      WHERE d.user_id=parent.user_id AND d.occurrence_id=parent.occurrence_id AND d.status='ready'
        AND d.id::text=c->>'document_id' AND d.file_name=c->>'file_name' AND d.version::text=c->>'document_version'
        AND dc.chunk_index::text=c->>'chunk_index' AND dc.locator=c->'locator'
        AND btrim(COALESCE(c->>'verbatim_text',''))<>''
        AND position((c->>'verbatim_text') IN dc.text)>0
        AND c->>'supports_id'='true' AND c->>'supports_en'='true'
    )
  ) THEN RETURN; END IF;
 ELSIF result_status IN('no_evidence','retrieval_failed','provider_failed','parse_failed','quota_exceeded') THEN
  IF result_id<>'-' OR result_en<>'-' OR result_citations<>'[]'::jsonb THEN RETURN; END IF;
 ELSE
  RETURN;
 END IF;
 IF result_revision_count NOT BETWEEN 0 AND 1 THEN RETURN; END IF;
 RETURN QUERY INSERT INTO public.answers(user_id,meeting_id,occurrence_id,question_id,answer_id,answer_en,status,citations,revision_count)
 VALUES(parent.user_id,parent.meeting_id,parent.occurrence_id,parent.id,result_id,result_en,result_status,result_citations,result_revision_count)
 ON CONFLICT(question_id) DO UPDATE SET answer_id=EXCLUDED.answer_id,answer_en=EXCLUDED.answer_en,status=EXCLUDED.status,citations=EXCLUDED.citations,revision_count=EXCLUDED.revision_count,updated_at=clock_timestamp()
 WHERE public.answers.user_id=(SELECT auth.uid()) RETURNING public.answers.*;
END $$;
CREATE OR REPLACE FUNCTION public.complete_assistance_segment(run_uuid uuid,segment_seq integer,result_status text,result_error_code text DEFAULT NULL)
RETURNS SETOF public.assistance_segment_status LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE parent public.meeting_runs; source public.transcript_segments;
BEGIN
 SELECT r.* INTO parent FROM public.meeting_runs r JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
  WHERE r.id=run_uuid AND r.user_id=(SELECT auth.uid()) AND o.status IN('active','awaiting_decision') FOR UPDATE OF r;
 IF NOT FOUND OR result_status NOT IN('completed','failed') THEN RETURN; END IF;
 IF result_status='failed' AND btrim(COALESCE(result_error_code,''))='' THEN RETURN; END IF;
 SELECT s.* INTO source FROM public.transcript_segments s WHERE s.run_id=parent.id AND s.seq=segment_seq AND s.user_id=parent.user_id;
 IF NOT FOUND THEN RETURN; END IF;
 RETURN QUERY INSERT INTO public.assistance_segment_status(user_id,meeting_id,occurrence_id,run_id,source_segment_id,source_seq,status,error_code)
 VALUES(parent.user_id,parent.meeting_id,parent.occurrence_id,parent.id,source.id,source.seq,result_status,CASE WHEN result_status='failed' THEN result_error_code ELSE NULL END)
 ON CONFLICT(source_segment_id) DO UPDATE SET status=EXCLUDED.status,error_code=EXCLUDED.error_code,updated_at=clock_timestamp()
 WHERE public.assistance_segment_status.user_id=(SELECT auth.uid()) RETURNING public.assistance_segment_status.*;
END $$;

CREATE OR REPLACE FUNCTION public.fail_missing_assistance_segments(run_uuid uuid,failure_code text DEFAULT 'assistance_queue_overflow')
RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE parent public.meeting_runs; affected integer;
BEGIN
 SELECT r.* INTO parent FROM public.meeting_runs r JOIN public.meeting_occurrences o ON o.id=r.occurrence_id
  WHERE r.id=run_uuid AND r.user_id=(SELECT auth.uid()) AND o.status IN('active','awaiting_decision') FOR UPDATE OF r;
 IF NOT FOUND OR btrim(failure_code)='' THEN RETURN 0; END IF;
 INSERT INTO public.assistance_segment_status(user_id,meeting_id,occurrence_id,run_id,source_segment_id,source_seq,status,error_code)
  SELECT s.user_id,s.meeting_id,s.occurrence_id,s.run_id,s.id,s.seq,'failed',btrim(failure_code)
  FROM public.transcript_segments s
  WHERE s.run_id=parent.id AND s.user_id=parent.user_id
   AND NOT EXISTS(SELECT 1 FROM public.assistance_segment_status a WHERE a.source_segment_id=s.id)
  ON CONFLICT(source_segment_id) DO NOTHING;
 GET DIAGNOSTICS affected=ROW_COUNT;
 RETURN affected;
END $$;

REVOKE ALL ON FUNCTION public.fail_missing_assistance_segments(uuid,text) FROM PUBLIC,anon;
GRANT EXECUTE ON FUNCTION public.fail_missing_assistance_segments(uuid,text) TO authenticated;

REVOKE ALL ON FUNCTION public.create_question(uuid,integer,text,text) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.search_document_chunks(uuid,text,integer) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.classify_document_evidence_state(uuid) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.put_answer(uuid,text,text,text,jsonb,integer) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.complete_assistance_segment(uuid,integer,text,text) FROM PUBLIC,anon;
GRANT EXECUTE ON FUNCTION public.create_question(uuid,integer,text,text) TO authenticated;
GRANT EXECUTE ON FUNCTION public.search_document_chunks(uuid,text,integer) TO authenticated;
GRANT EXECUTE ON FUNCTION public.classify_document_evidence_state(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.put_answer(uuid,text,text,text,jsonb,integer) TO authenticated;
GRANT EXECUTE ON FUNCTION public.complete_assistance_segment(uuid,integer,text,text) TO authenticated;
NOTIFY pgrst,'reload schema';
COMMIT;
