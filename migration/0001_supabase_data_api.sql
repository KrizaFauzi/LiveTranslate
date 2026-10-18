-- Run in Supabase SQL Editor. Safe for fresh projects and the old 0000 schema.
-- Keeps the historical 0000 migration and any existing runtime role untouched.
BEGIN;

CREATE TABLE IF NOT EXISTS public.profiles (
  id uuid PRIMARY KEY REFERENCES auth.users(id) ON DELETE CASCADE,
  display_name varchar(100) NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT profiles_display_name_check CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 100)
);

ALTER TABLE public.profiles ENABLE ROW LEVEL SECURITY;
REVOKE ALL ON public.profiles FROM PUBLIC, anon, authenticated;
GRANT USAGE ON SCHEMA public TO authenticated;
GRANT SELECT ON public.profiles TO authenticated;
GRANT UPDATE (display_name) ON public.profiles TO authenticated;

DROP POLICY IF EXISTS profiles_select_own ON public.profiles;
CREATE POLICY profiles_select_own ON public.profiles FOR SELECT TO authenticated
  USING ((SELECT auth.uid()) = id);
DROP POLICY IF EXISTS profiles_update_own ON public.profiles;
CREATE POLICY profiles_update_own ON public.profiles FOR UPDATE TO authenticated
  USING ((SELECT auth.uid()) = id) WITH CHECK ((SELECT auth.uid()) = id);

CREATE OR REPLACE FUNCTION public.livetranslate_display_name(metadata jsonb)
RETURNS text LANGUAGE sql IMMUTABLE SET search_path = '' AS $$
  SELECT CASE WHEN jsonb_typeof(metadata->'display_name') = 'string'
    THEN coalesce(nullif(left(btrim(metadata->>'display_name'), 100), ''), 'Pengguna')
    ELSE 'Pengguna' END;
$$;
REVOKE ALL ON FUNCTION public.livetranslate_display_name(jsonb) FROM PUBLIC;

CREATE OR REPLACE FUNCTION public.livetranslate_create_profile()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path = '' AS $$
BEGIN
  INSERT INTO public.profiles(id, display_name)
  VALUES (NEW.id, public.livetranslate_display_name(NEW.raw_user_meta_data));
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.livetranslate_create_profile() FROM PUBLIC;
DROP TRIGGER IF EXISTS livetranslate_auth_user_created ON auth.users;
CREATE TRIGGER livetranslate_auth_user_created AFTER INSERT ON auth.users
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_create_profile();

CREATE OR REPLACE FUNCTION public.livetranslate_touch_profile()
RETURNS trigger LANGUAGE plpgsql SET search_path = '' AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$;
REVOKE ALL ON FUNCTION public.livetranslate_touch_profile() FROM PUBLIC;
DROP TRIGGER IF EXISTS livetranslate_profile_updated ON public.profiles;
CREATE TRIGGER livetranslate_profile_updated BEFORE UPDATE ON public.profiles
  FOR EACH ROW EXECUTE FUNCTION public.livetranslate_touch_profile();

-- Existing users also need profiles. Re-running does not overwrite their names.
INSERT INTO public.profiles(id, display_name)
SELECT id, public.livetranslate_display_name(raw_user_meta_data) FROM auth.users
ON CONFLICT (id) DO NOTHING;

NOTIFY pgrst, 'reload schema';
COMMIT;
