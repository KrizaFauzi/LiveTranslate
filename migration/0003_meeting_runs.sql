-- Apply after 0002 through Supabase SQL Editor. No service_role key is needed.
-- One row per capture session. Audio bytes are never stored: only run metadata.
BEGIN;
CREATE TABLE IF NOT EXISTS public.meeting_runs (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL DEFAULT auth.uid() REFERENCES auth.users(id) ON DELETE CASCADE,
  meeting_id uuid NOT NULL REFERENCES public.meeting_sessions(id) ON DELETE CASCADE,
  status text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'stopped', 'failed')),
  bytes_received bigint NOT NULL DEFAULT 0 CHECK (bytes_received >= 0),
  chunks_received integer NOT NULL DEFAULT 0 CHECK (chunks_received >= 0),
  started_at timestamptz NOT NULL DEFAULT now(),
  ended_at timestamptz
);
CREATE INDEX IF NOT EXISTS meeting_runs_user_started_idx ON public.meeting_runs(user_id, started_at DESC);
ALTER TABLE public.meeting_runs ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.meeting_runs FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.meeting_runs TO authenticated;
GRANT INSERT (user_id, meeting_id) ON public.meeting_runs TO authenticated;
GRANT UPDATE (status, bytes_received, chunks_received, ended_at) ON public.meeting_runs TO authenticated;
DROP POLICY IF EXISTS meeting_runs_select_own ON public.meeting_runs;
CREATE POLICY meeting_runs_select_own ON public.meeting_runs FOR SELECT TO authenticated
  USING ((SELECT auth.uid()) = user_id);
DROP POLICY IF EXISTS meeting_runs_insert_own ON public.meeting_runs;
CREATE POLICY meeting_runs_insert_own ON public.meeting_runs FOR INSERT TO authenticated
  WITH CHECK ((SELECT auth.uid()) = user_id);
DROP POLICY IF EXISTS meeting_runs_update_own ON public.meeting_runs;
CREATE POLICY meeting_runs_update_own ON public.meeting_runs FOR UPDATE TO authenticated
  USING ((SELECT auth.uid()) = user_id) WITH CHECK ((SELECT auth.uid()) = user_id);
-- Atomic accounting: concurrent chunks cannot lose an increment.
CREATE OR REPLACE FUNCTION public.append_audio_chunk(run_uuid uuid, chunk_bytes integer)
RETURNS setof public.meeting_runs
LANGUAGE sql
SECURITY INVOKER
SET search_path = public
AS $$
  UPDATE public.meeting_runs r
     SET bytes_received = r.bytes_received + GREATEST(chunk_bytes, 0),
         chunks_received = r.chunks_received + 1
   WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) AND r.status = 'active'
  RETURNING r.*;
$$;
CREATE OR REPLACE FUNCTION public.stop_audio_run(run_uuid uuid)
RETURNS setof public.meeting_runs
LANGUAGE sql
SECURITY INVOKER
SET search_path = public
AS $$
  UPDATE public.meeting_runs r
     SET status = 'stopped', ended_at = now()
   WHERE r.id = run_uuid AND r.user_id = (SELECT auth.uid()) AND r.status = 'active'
  RETURNING r.*;
$$;
REVOKE ALL ON FUNCTION public.append_audio_chunk(uuid, integer) FROM PUBLIC, anon;
REVOKE ALL ON FUNCTION public.stop_audio_run(uuid) FROM PUBLIC, anon;
GRANT EXECUTE ON FUNCTION public.append_audio_chunk(uuid, integer) TO authenticated;
GRANT EXECUTE ON FUNCTION public.stop_audio_run(uuid) TO authenticated;
NOTIFY pgrst, 'reload schema';
COMMIT;
