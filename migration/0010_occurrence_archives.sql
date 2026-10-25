-- Apply after 0009. Permanent complete archive manifests and post-meeting save/discard RPCs.
BEGIN;
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS public.archives (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
  occurrence_id uuid NOT NULL REFERENCES public.meeting_occurrences(id) ON DELETE CASCADE,
  manifest jsonb NOT NULL,
  status text NOT NULL DEFAULT 'committed' CHECK (status IN ('committed', 'partial_failed', 'deleted')),
  committed_at timestamptz NOT NULL DEFAULT now(),
  deleted_at timestamptz,
  CONSTRAINT archives_occurrence_key UNIQUE (occurrence_id),
  CONSTRAINT archives_delete_fields_check CHECK (
    (status = 'deleted' AND deleted_at IS NOT NULL) OR
    (status <> 'deleted' AND deleted_at IS NULL)
  )
);
CREATE INDEX IF NOT EXISTS archives_user_committed_idx
  ON public.archives(user_id, committed_at DESC, id DESC);
ALTER TABLE public.archives ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.archives FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.archives TO authenticated;
DROP POLICY IF EXISTS archives_select_own ON public.archives;
CREATE POLICY archives_select_own ON public.archives
  FOR SELECT TO authenticated USING (user_id = (SELECT auth.uid()));

CREATE TABLE IF NOT EXISTS public.storage_cleanup_jobs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL,
  occurrence_id uuid NOT NULL,
  archive_id uuid,
  bucket_id text NOT NULL DEFAULT 'meeting-documents',
  object_path text NOT NULL,
  reason text NOT NULL CHECK (reason IN ('discard','expiry','archive_delete')),
  status text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','completed')),
  attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  last_error text,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  completed_at timestamptz,
  UNIQUE (bucket_id, object_path),
  CONSTRAINT storage_cleanup_completion_check CHECK (
    (status='completed' AND completed_at IS NOT NULL AND last_error IS NULL)
    OR (status='pending' AND completed_at IS NULL)
  )
);
CREATE INDEX IF NOT EXISTS storage_cleanup_pending_idx
  ON public.storage_cleanup_jobs(status, created_at, id);
ALTER TABLE public.storage_cleanup_jobs ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.storage_cleanup_jobs FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.storage_cleanup_jobs TO authenticated;
DROP POLICY IF EXISTS storage_cleanup_jobs_select_own ON public.storage_cleanup_jobs;
CREATE POLICY storage_cleanup_jobs_select_own ON public.storage_cleanup_jobs
  FOR SELECT TO authenticated USING (user_id=(SELECT auth.uid()));

CREATE OR REPLACE FUNCTION public.complete_storage_cleanup_jobs(cleaned_paths text[])
RETURNS bigint LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE changed bigint;
BEGIN
  UPDATE public.storage_cleanup_jobs j
    SET status='completed', completed_at=clock_timestamp(), updated_at=clock_timestamp(), last_error=NULL
    WHERE j.user_id=(SELECT auth.uid()) AND j.status='pending' AND j.object_path=ANY(cleaned_paths);
  GET DIAGNOSTICS changed = ROW_COUNT;
  RETURN changed;
END $$;

CREATE OR REPLACE FUNCTION public.archive_manifest_objects(occurrence_uuid uuid)
RETURNS jsonb LANGUAGE sql SECURITY DEFINER STABLE SET search_path='' AS $$
  SELECT jsonb_build_array(
    jsonb_build_object('type','transcript_segments','count',(SELECT count(*) FROM public.transcript_segments x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.run_id,x.seq),'[]')::text FROM public.transcript_segments x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','translations','count',(SELECT count(*) FROM public.translations x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.run_id,x.source_seq,x.source_revision),'[]')::text FROM public.translations x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','documents','count',(SELECT count(*) FROM public.documents x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.id),'[]')::text FROM public.documents x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','document_chunks','count',(SELECT count(*) FROM public.document_chunks x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.document_id,x.chunk_index),'[]')::text FROM public.document_chunks x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','questions','count',(SELECT count(*) FROM public.questions x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.created_at,x.id),'[]')::text FROM public.questions x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','answers','count',(SELECT count(*) FROM public.answers x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.created_at,x.id),'[]')::text FROM public.answers x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex')),
    jsonb_build_object('type','assistance_segment_status','count',(SELECT count(*) FROM public.assistance_segment_status x WHERE x.occurrence_id=occurrence_uuid),'sha256',encode(public.digest(convert_to((SELECT COALESCE(jsonb_agg(to_jsonb(x) ORDER BY x.run_id,x.source_seq),'[]')::text FROM public.assistance_segment_status x WHERE x.occurrence_id=occurrence_uuid),'UTF8'),'sha256'),'hex'))
  );
$$;
REVOKE ALL ON FUNCTION public.archive_manifest_objects(uuid) FROM PUBLIC, anon, authenticated;

CREATE OR REPLACE FUNCTION public.commit_meeting_archive(occurrence_uuid uuid)
RETURNS SETOF public.archives
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE occurrence public.meeting_occurrences;
DECLARE manifest_value jsonb;
DECLARE archive_status text;
BEGIN
  SELECT o.* INTO occurrence FROM public.meeting_occurrences o
    WHERE o.id = occurrence_uuid AND o.user_id = (SELECT auth.uid()) FOR UPDATE;
  IF NOT FOUND THEN RETURN; END IF;
  IF occurrence.status = 'archived' THEN
    RETURN QUERY SELECT a.* FROM public.archives a WHERE a.occurrence_id=occurrence.id AND a.user_id=occurrence.user_id AND a.status<>'deleted';
    RETURN;
  END IF;
  IF occurrence.status <> 'awaiting_decision' OR occurrence.staging_expires_at <= clock_timestamp() THEN RETURN; END IF;
  IF EXISTS(SELECT 1 FROM public.meeting_runs r WHERE r.occurrence_id=occurrence.id AND (r.status NOT IN ('stopped','failed') OR (r.save_transcript AND r.transcript_state<>'saved')))
    OR EXISTS(SELECT 1 FROM public.documents d WHERE d.occurrence_id=occurrence.id AND d.status NOT IN ('ready','failed'))
    OR EXISTS(SELECT 1 FROM public.questions q LEFT JOIN public.answers a ON a.question_id=q.id WHERE q.occurrence_id=occurrence.id AND a.id IS NULL)
    OR (SELECT count(*) FROM public.translations t WHERE t.occurrence_id=occurrence.id) <> (SELECT count(*) FROM public.transcript_segments s WHERE s.occurrence_id=occurrence.id)
    OR (SELECT count(*) FROM public.assistance_segment_status j WHERE j.occurrence_id=occurrence.id) <> (SELECT count(*) FROM public.transcript_segments s WHERE s.occurrence_id=occurrence.id)
  THEN RETURN; END IF;
  archive_status := CASE WHEN
    EXISTS(SELECT 1 FROM public.meeting_runs r WHERE r.occurrence_id=occurrence.id AND r.status='failed')
    OR EXISTS(SELECT 1 FROM public.documents d WHERE d.occurrence_id=occurrence.id AND d.status='failed')
    OR EXISTS(SELECT 1 FROM public.translations t WHERE t.occurrence_id=occurrence.id AND t.status='failed')
    OR EXISTS(SELECT 1 FROM public.assistance_segment_status j WHERE j.occurrence_id=occurrence.id AND j.status='failed')
    OR EXISTS(SELECT 1 FROM public.answers a WHERE a.occurrence_id=occurrence.id AND a.status IN ('retrieval_failed','provider_failed','parse_failed','quota_exceeded'))
    THEN 'partial_failed' ELSE 'committed' END;
  manifest_value := jsonb_build_object(
    'schema_version',2,'occurrence_id',occurrence.id,
    'objects',public.archive_manifest_objects(occurrence.id)
  );
  INSERT INTO public.archives(user_id,occurrence_id,manifest,status) VALUES(occurrence.user_id,occurrence.id,manifest_value,archive_status) ON CONFLICT(occurrence_id) DO NOTHING;
  UPDATE public.meeting_occurrences SET status='archived',consent_given_at=clock_timestamp(),staging_expires_at=NULL WHERE id=occurrence.id;
  RETURN QUERY SELECT a.* FROM public.archives a WHERE a.occurrence_id=occurrence.id AND a.user_id=occurrence.user_id;
END $$;

CREATE OR REPLACE FUNCTION public.verify_meeting_archive(archive_uuid uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER STABLE SET search_path='' AS $$
DECLARE target public.archives;
BEGIN
  SELECT a.* INTO target FROM public.archives a
    WHERE a.id=archive_uuid AND a.user_id=(SELECT auth.uid()) AND a.status IN ('committed','partial_failed');
  IF NOT FOUND THEN RETURN false; END IF;
  RETURN target.manifest->'objects'=public.archive_manifest_objects(target.occurrence_id);
END $$;

CREATE OR REPLACE FUNCTION public.discard_meeting_occurrence(occurrence_uuid uuid)
RETURNS SETOF public.meeting_occurrences
LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
DECLARE occurrence public.meeting_occurrences;
DECLARE run_row public.meeting_runs;
BEGIN
  SELECT o.* INTO occurrence FROM public.meeting_occurrences o
    WHERE o.id = occurrence_uuid AND o.user_id = (SELECT auth.uid())
    FOR UPDATE;
  IF NOT FOUND OR occurrence.status IN ('archived', 'discarded', 'discarding') THEN RETURN; END IF;
  IF occurrence.status = 'awaiting_decision'
    AND occurrence.staging_expires_at <= clock_timestamp() THEN RETURN; END IF;
  UPDATE public.meeting_occurrences o
    SET status = 'discarding', ended_at = COALESCE(o.ended_at, clock_timestamp()),
        staging_expires_at = COALESCE(o.staging_expires_at, clock_timestamp())
    WHERE o.id = occurrence.id;
  FOR run_row IN SELECT r.* FROM public.meeting_runs r WHERE r.occurrence_id = occurrence.id LOOP
    IF run_row.save_transcript AND run_row.transcript_state <> 'deleted' THEN
      PERFORM * FROM public.delete_transcript(run_row.id);
    END IF;
  END LOOP;
  INSERT INTO public.storage_cleanup_jobs(user_id,occurrence_id,object_path,reason)
    SELECT d.user_id,d.occurrence_id,d.object_path,'discard' FROM public.documents d
    WHERE d.occurrence_id=occurrence.id
    ON CONFLICT(bucket_id,object_path) DO NOTHING;
  DELETE FROM public.documents d WHERE d.occurrence_id=occurrence.id;
  UPDATE public.meeting_occurrences o
    SET status = 'discarded', staging_expires_at = NULL, consent_given_at = NULL
    WHERE o.id = occurrence.id;
  RETURN QUERY SELECT o.* FROM public.meeting_occurrences o WHERE o.id = occurrence.id;
END $$;

CREATE OR REPLACE FUNCTION public.delete_meeting_archive(archive_uuid uuid)
RETURNS boolean LANGUAGE plpgsql SECURITY DEFINER SET search_path='' AS $$
DECLARE target public.archives;
BEGIN
 SELECT a.* INTO target FROM public.archives a
   WHERE a.id=archive_uuid AND a.user_id=(SELECT auth.uid()) AND a.status IN ('committed','partial_failed','deleted') FOR UPDATE;
 IF NOT FOUND THEN RETURN false; END IF;
 IF target.status='deleted' THEN RETURN true; END IF;
 INSERT INTO public.storage_cleanup_jobs(user_id,occurrence_id,archive_id,object_path,reason)
   SELECT d.user_id,d.occurrence_id,target.id,d.object_path,'archive_delete' FROM public.documents d
   WHERE d.occurrence_id=target.occurrence_id
   ON CONFLICT(bucket_id,object_path) DO NOTHING;
 UPDATE public.meeting_runs r SET transcript_state='deleted'
   WHERE r.occurrence_id=target.occurrence_id AND r.save_transcript AND r.transcript_state<>'deleted';
 DELETE FROM public.documents d WHERE d.occurrence_id=target.occurrence_id;
 DELETE FROM public.questions q WHERE q.occurrence_id=target.occurrence_id;
 DELETE FROM public.translations t WHERE t.occurrence_id=target.occurrence_id;
 DELETE FROM public.assistance_segment_status s WHERE s.occurrence_id=target.occurrence_id;
 UPDATE public.archives a SET status='deleted',deleted_at=clock_timestamp() WHERE a.id=target.id;
 RETURN true;
END $$;

REVOKE ALL ON FUNCTION public.complete_storage_cleanup_jobs(text[]) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.complete_storage_cleanup_jobs(text[]) TO authenticated;
REVOKE ALL ON FUNCTION public.commit_meeting_archive(uuid) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.verify_meeting_archive(uuid) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.discard_meeting_occurrence(uuid) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.delete_meeting_archive(uuid) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.commit_meeting_archive(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.verify_meeting_archive(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.discard_meeting_occurrence(uuid) TO authenticated;
GRANT EXECUTE ON FUNCTION public.delete_meeting_archive(uuid) TO authenticated;
NOTIFY pgrst, 'reload schema';
COMMIT;
