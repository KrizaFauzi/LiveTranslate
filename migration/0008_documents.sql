-- Apply after 0007. Meeting-scoped source documents and extracted citation chunks.
BEGIN;

CREATE TABLE IF NOT EXISTS public.documents (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL DEFAULT auth.uid() REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
  file_name text NOT NULL CHECK (length(file_name) BETWEEN 1 AND 255),
  media_type text NOT NULL,
  byte_size bigint NOT NULL CHECK (byte_size BETWEEN 1 AND 20971520),
  sha256 text NOT NULL CHECK (sha256 ~ '^[0-9a-f]{64}$'),
  version integer NOT NULL CHECK (version > 0),
  object_path text NOT NULL,
  status text NOT NULL DEFAULT 'uploading' CHECK (status IN ('uploading','extracting','ready','failed')),
  error_code text,
  quality_flags text[] NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (occurrence_id, file_name, version),
  UNIQUE (object_path),
  CONSTRAINT document_status_check CHECK ((status = 'failed') = (error_code IS NOT NULL))
);

CREATE TABLE IF NOT EXISTS public.document_chunks (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
  document_id uuid NOT NULL REFERENCES public.documents(id) ON DELETE CASCADE,
  chunk_index integer NOT NULL CHECK (chunk_index >= 0),
  locator jsonb NOT NULL,
  text text NOT NULL CHECK (length(btrim(text)) > 0 AND length(text) <= 12000),
  search_vector tsvector GENERATED ALWAYS AS (to_tsvector('simple', text)) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (document_id, chunk_index)
);
CREATE INDEX IF NOT EXISTS document_chunks_occurrence_idx ON public.document_chunks(occurrence_id, document_id, chunk_index);
CREATE INDEX IF NOT EXISTS document_chunks_search_idx ON public.document_chunks USING gin(search_vector);

ALTER TABLE public.documents ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.document_chunks ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.documents, public.document_chunks FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.documents, public.document_chunks TO authenticated;
DROP POLICY IF EXISTS documents_select_own ON public.documents;
CREATE POLICY documents_select_own ON public.documents FOR SELECT TO authenticated USING (user_id = (SELECT auth.uid()));
DROP POLICY IF EXISTS document_chunks_select_own ON public.document_chunks;
CREATE POLICY document_chunks_select_own ON public.document_chunks FOR SELECT TO authenticated USING (user_id = (SELECT auth.uid()));

CREATE OR REPLACE FUNCTION public.create_meeting_document(
  occurrence_uuid uuid, document_name text, document_media_type text,
  document_size bigint, document_sha256 text
) RETURNS SETOF public.documents
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE parent public.meeting_occurrences; next_version integer; document_uuid uuid := gen_random_uuid(); clean_name text;
BEGIN
  clean_name := btrim(document_name);
  SELECT o.* INTO parent FROM public.meeting_occurrences o
    WHERE o.id = occurrence_uuid AND o.user_id = (SELECT auth.uid())
      AND o.status IN ('pending_documents','active') FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  IF clean_name = '' OR length(clean_name) > 255 OR document_size < 1 OR document_size > 20971520
    OR document_sha256 !~ '^[0-9a-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE = '22023', MESSAGE = 'invalid document metadata';
  END IF;
  IF (SELECT count(*) FROM public.documents d WHERE d.occurrence_id = parent.id) >= 10
    OR COALESCE((SELECT sum(d.byte_size) FROM public.documents d WHERE d.occurrence_id = parent.id),0) + document_size > 104857600 THEN
    RAISE EXCEPTION USING ERRCODE = '54000', MESSAGE = 'document occurrence limit exceeded';
  END IF;
  SELECT COALESCE(max(d.version),0)+1 INTO next_version FROM public.documents d
    WHERE d.occurrence_id = parent.id AND d.file_name = clean_name;
  RETURN QUERY INSERT INTO public.documents(
    id,user_id,meeting_id,occurrence_id,file_name,media_type,byte_size,sha256,version,object_path
  ) VALUES (
    document_uuid,parent.user_id,parent.meeting_id,parent.id,clean_name,document_media_type,document_size,document_sha256,next_version,
    parent.user_id::text||'/'||parent.id::text||'/'||document_uuid::text||'/'||replace(clean_name,'/','_')
  ) RETURNING *;
END $$;

CREATE OR REPLACE FUNCTION public.commit_document_extraction(
  document_uuid uuid, extraction_chunks jsonb, extraction_flags text[] DEFAULT '{}'
) RETURNS SETOF public.documents
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE parent public.documents; item jsonb; item_index integer := 0;
BEGIN
  SELECT d.* INTO parent FROM public.documents d JOIN public.meeting_occurrences o ON o.id=d.occurrence_id
    WHERE d.id=document_uuid AND d.user_id=(SELECT auth.uid()) AND o.status IN ('pending_documents','active') FOR UPDATE OF d;
  IF NOT FOUND THEN RETURN; END IF;
  DELETE FROM public.document_chunks c WHERE c.document_id=parent.id;
  FOR item IN SELECT value FROM jsonb_array_elements(extraction_chunks) LOOP
    IF length(COALESCE(item->>'text','')) > 12000 OR btrim(COALESCE(item->>'text','')) = '' OR item->'locator' IS NULL THEN
      RAISE EXCEPTION USING ERRCODE='22023', MESSAGE='invalid document chunk';
    END IF;
    INSERT INTO public.document_chunks(user_id,meeting_id,occurrence_id,document_id,chunk_index,locator,text)
      VALUES(parent.user_id,parent.meeting_id,parent.occurrence_id,parent.id,item_index,item->'locator',item->>'text');
    item_index := item_index + 1;
    IF item_index > 5000 THEN RAISE EXCEPTION USING ERRCODE='54000', MESSAGE='too many document chunks'; END IF;
  END LOOP;
  UPDATE public.documents SET status='ready',error_code=NULL,quality_flags=extraction_flags,updated_at=clock_timestamp() WHERE id=parent.id;
  RETURN QUERY SELECT * FROM public.documents d WHERE d.id=parent.id;
END $$;

CREATE OR REPLACE FUNCTION public.fail_document_extraction(document_uuid uuid, failure_code text)
RETURNS SETOF public.documents LANGUAGE sql SECURITY DEFINER SET search_path='' AS $$
  UPDATE public.documents d SET status='failed',error_code=btrim(failure_code),updated_at=clock_timestamp()
  WHERE d.id=document_uuid AND d.user_id=(SELECT auth.uid()) AND length(btrim(failure_code))>0 RETURNING d.*;
$$;

REVOKE ALL ON FUNCTION public.create_meeting_document(uuid,text,text,bigint,text) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.commit_document_extraction(uuid,jsonb,text[]) FROM PUBLIC,anon;
REVOKE ALL ON FUNCTION public.fail_document_extraction(uuid,text) FROM PUBLIC,anon;
GRANT EXECUTE ON FUNCTION public.create_meeting_document(uuid,text,text,bigint,text) TO authenticated;
GRANT EXECUTE ON FUNCTION public.commit_document_extraction(uuid,jsonb,text[]) TO authenticated;
GRANT EXECUTE ON FUNCTION public.fail_document_extraction(uuid,text) TO authenticated;

DO $$ BEGIN
  IF to_regclass('storage.buckets') IS NOT NULL THEN
    INSERT INTO storage.buckets(id,name,public,file_size_limit,allowed_mime_types)
    VALUES('meeting-documents','meeting-documents',false,20971520,ARRAY['application/pdf','application/vnd.openxmlformats-officedocument.wordprocessingml.document','application/vnd.openxmlformats-officedocument.spreadsheetml.sheet','text/csv'])
    ON CONFLICT (id) DO UPDATE SET public=false,file_size_limit=20971520,allowed_mime_types=EXCLUDED.allowed_mime_types;
    EXECUTE 'DROP POLICY IF EXISTS livetranslate_documents_insert_own ON storage.objects';
    EXECUTE 'CREATE POLICY livetranslate_documents_insert_own ON storage.objects FOR INSERT TO authenticated WITH CHECK (bucket_id = ''meeting-documents'' AND (storage.foldername(name))[1] = (SELECT auth.uid())::text)';
    EXECUTE 'DROP POLICY IF EXISTS livetranslate_documents_select_own ON storage.objects';
    EXECUTE 'CREATE POLICY livetranslate_documents_select_own ON storage.objects FOR SELECT TO authenticated USING (bucket_id = ''meeting-documents'' AND (storage.foldername(name))[1] = (SELECT auth.uid())::text)';
    EXECUTE 'DROP POLICY IF EXISTS livetranslate_documents_delete_own ON storage.objects';
    EXECUTE 'CREATE POLICY livetranslate_documents_delete_own ON storage.objects FOR DELETE TO authenticated USING (bucket_id = ''meeting-documents'' AND (storage.foldername(name))[1] = (SELECT auth.uid())::text)';
  END IF;
END $$;

NOTIFY pgrst, 'reload schema';
COMMIT;
