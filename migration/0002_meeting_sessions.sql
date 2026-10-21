-- Apply after 0001 through Supabase SQL Editor. No service_role key is needed.
BEGIN;
CREATE TABLE IF NOT EXISTS public.meeting_sessions (
  id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id uuid NOT NULL DEFAULT auth.uid() REFERENCES auth.users(id) ON DELETE CASCADE,
  provider text NOT NULL,
  provider_host text NOT NULL,
  provider_room_id text NOT NULL,
  meeting_key text GENERATED ALWAYS AS (provider || ':' || provider_host || ':' || provider_room_id) STORED,
  canonical_url text GENERATED ALWAYS AS (
    'https://' || provider_host || CASE WHEN provider = 'zoom'
      THEN '/wc/' || provider_room_id || '/join' ELSE '/' || provider_room_id END
  ) STORED,
  created_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT meeting_sessions_identity_check CHECK (
    (provider = 'google_meet' AND provider_host = 'meet.google.com' AND provider_room_id ~ '^[a-z]{3}-[a-z]{4}-[a-z]{3}$') OR
    (provider = 'zoom' AND provider_host = 'zoom.us' AND provider_room_id ~ '^[0-9]{9,11}$') OR
    (provider = 'jitsi' AND provider_host = 'meet.jit.si' AND provider_room_id ~ '^[a-z0-9_-]{1,128}$'
      AND lower(provider_room_id) NOT IN ('static','libs','css','images','sounds','lang','fonts','external_api','http-bind','xmpp-websocket','about','privacy','terms','login','logout','register','settings','help'))
  ),
  CONSTRAINT meeting_sessions_user_key UNIQUE (user_id, meeting_key)
);
CREATE INDEX IF NOT EXISTS meeting_sessions_user_created_idx ON public.meeting_sessions(user_id, created_at DESC, id DESC);
ALTER TABLE public.meeting_sessions ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.meeting_sessions FROM PUBLIC, anon, authenticated;
GRANT SELECT ON public.meeting_sessions TO authenticated;
GRANT INSERT (user_id, provider, provider_host, provider_room_id) ON public.meeting_sessions TO authenticated;
DROP POLICY IF EXISTS meeting_sessions_select_own ON public.meeting_sessions;
CREATE POLICY meeting_sessions_select_own ON public.meeting_sessions FOR SELECT TO authenticated
  USING ((SELECT auth.uid()) = user_id);
DROP POLICY IF EXISTS meeting_sessions_insert_own ON public.meeting_sessions;
CREATE POLICY meeting_sessions_insert_own ON public.meeting_sessions FOR INSERT TO authenticated
  WITH CHECK ((SELECT auth.uid()) = user_id);
-- Deliberately no UPDATE or DELETE grant: a room's persisted identity is immutable.
NOTIFY pgrst, 'reload schema';
COMMIT;
