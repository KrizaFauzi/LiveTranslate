-- Local isolated PostgreSQL harness: emulate only Supabase roles/auth.uid().
CREATE ROLE anon NOLOGIN;
CREATE ROLE authenticated NOLOGIN;
CREATE SCHEMA auth;
CREATE TABLE auth.users (id uuid PRIMARY KEY);
CREATE FUNCTION auth.uid() RETURNS uuid LANGUAGE sql STABLE AS $$
  SELECT nullif(current_setting('request.jwt.claim.sub', true), '')::uuid;
$$;
GRANT USAGE ON SCHEMA auth, public TO authenticated;
GRANT EXECUTE ON FUNCTION auth.uid() TO authenticated;
\i /migrations/0002_meeting_sessions.sql
\i /migrations/0002_meeting_sessions.sql
INSERT INTO auth.users VALUES ('11111111-2222-4333-8444-555555555555'), ('66666666-2222-4333-8444-555555555555');
SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';
INSERT INTO public.meeting_sessions (user_id, provider, provider_host, provider_room_id)
VALUES (auth.uid(), 'google_meet', 'meet.google.com', 'abc-defg-hij');
CREATE TEMP TABLE first_identity AS SELECT id FROM public.meeting_sessions;
INSERT INTO public.meeting_sessions (user_id, provider, provider_host, provider_room_id)
VALUES (auth.uid(), 'google_meet', 'meet.google.com', 'abc-defg-hij')
ON CONFLICT (user_id, meeting_key) DO NOTHING;
DO $$ BEGIN
  IF (SELECT count(*) FROM public.meeting_sessions) <> 1 THEN RAISE EXCEPTION 'duplicate session'; END IF;
  IF NOT EXISTS (SELECT 1 FROM public.meeting_sessions m JOIN first_identity f USING (id)
    WHERE meeting_key='google_meet:meet.google.com:abc-defg-hij'
      AND canonical_url='https://meet.google.com/abc-defg-hij') THEN RAISE EXCEPTION 'identity changed'; END IF;
  BEGIN
    INSERT INTO public.meeting_sessions(user_id,provider,provider_host,provider_room_id)
    VALUES ('66666666-2222-4333-8444-555555555555','google_meet','meet.google.com','abc-defg-hij');
    RAISE EXCEPTION 'foreign insert was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    INSERT INTO public.meeting_sessions(user_id,provider,provider_host,provider_room_id)
    VALUES (auth.uid(),'google_meet','evil.test','abc-defg-hij');
    RAISE EXCEPTION 'invalid identity was allowed';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    INSERT INTO public.meeting_sessions(user_id,provider,provider_host,provider_room_id)
    VALUES (auth.uid(),'jitsi','meet.jit.si','login');
    RAISE EXCEPTION 'reserved Jitsi page was allowed';
  EXCEPTION WHEN check_violation THEN NULL; END;
  BEGIN
    UPDATE public.meeting_sessions SET provider_room_id='xyz-uvwx-rst';
    RAISE EXCEPTION 'update was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    DELETE FROM public.meeting_sessions;
    RAISE EXCEPTION 'delete was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF (SELECT count(*) FROM public.meeting_sessions) <> 0 THEN RAISE EXCEPTION 'foreign session visible'; END IF;
END $$;
INSERT INTO public.meeting_sessions(user_id,provider,provider_host,provider_room_id)
VALUES (auth.uid(),'google_meet','meet.google.com','abc-defg-hij');
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.meeting_sessions m JOIN first_identity f USING(id)) THEN RAISE EXCEPTION 'shared ID across accounts'; END IF;
  IF (SELECT count(*) FROM public.meeting_sessions) <> 1 THEN RAISE EXCEPTION 'own session missing'; END IF;
END $$;
RESET ROLE;
SET ROLE anon;
DO $$ BEGIN
  BEGIN
    PERFORM * FROM public.meeting_sessions;
    RAISE EXCEPTION 'anonymous read was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;
SELECT 'meeting identity and RLS checks passed' AS result;
