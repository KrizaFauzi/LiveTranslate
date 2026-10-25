-- P0-1 regression: occurrence identity, ownership, lifecycle, and run binding.
\i /migrations/0006_meeting_occurrences.sql
\i /migrations/0006_meeting_occurrences.sql

SET ROLE authenticated;
SET request.jwt.claim.sub = '11111111-2222-4333-8444-555555555555';

DO $$ BEGIN
  IF EXISTS (
    SELECT 1 FROM public.meeting_runs
    WHERE occurrence_id IS NULL
  ) THEN RAISE EXCEPTION 'existing run was not backfilled'; END IF;
  IF EXISTS (
    SELECT 1 FROM public.meeting_runs r
    JOIN public.meeting_occurrences o ON o.id = r.occurrence_id
    WHERE r.user_id IS DISTINCT FROM o.user_id OR r.meeting_id IS DISTINCT FROM o.meeting_id
  ) THEN RAISE EXCEPTION 'backfilled occurrence ownership mismatch'; END IF;
END $$;

CREATE TEMP TABLE owner_meeting AS
  SELECT id FROM public.meeting_sessions WHERE user_id = auth.uid() LIMIT 1;
CREATE TEMP TABLE connected_occurrence AS
  SELECT * FROM public.connect_meeting_occurrence((SELECT id FROM owner_meeting));

DO $$ BEGIN
  IF (SELECT count(*) FROM connected_occurrence) <> 1 THEN RAISE EXCEPTION 'connect did not create occurrence'; END IF;
  IF (SELECT status FROM connected_occurrence) <> 'pending_documents' THEN RAISE EXCEPTION 'initial occurrence state wrong'; END IF;
  IF (SELECT user_id FROM connected_occurrence) <> auth.uid() THEN RAISE EXCEPTION 'occurrence owner wrong'; END IF;
END $$;

SELECT * FROM public.activate_meeting_occurrence((SELECT id FROM connected_occurrence));
SELECT * FROM public.finish_meeting_occurrence((SELECT id FROM connected_occurrence));

DO $$ BEGIN
  IF (SELECT status FROM public.meeting_occurrences WHERE id = (SELECT id FROM connected_occurrence)) <> 'awaiting_decision' THEN
    RAISE EXCEPTION 'finish did not await archive decision';
  END IF;
  IF (SELECT staging_expires_at FROM public.meeting_occurrences WHERE id = (SELECT id FROM connected_occurrence)) IS NULL THEN
    RAISE EXCEPTION 'staging deadline missing';
  END IF;
  IF (SELECT consent_given_at FROM public.meeting_occurrences WHERE id = (SELECT id FROM connected_occurrence)) IS NOT NULL THEN
    RAISE EXCEPTION 'archive consent was inferred';
  END IF;
END $$;

-- Reusing one room creates a distinct occurrence, never a shared day/session context.
DO $$ BEGIN
  IF (SELECT count(*) FROM public.connect_meeting_occurrence((SELECT id FROM owner_meeting))) <> 1 THEN
    RAISE EXCEPTION 'second occurrence not created';
  END IF;
  IF (SELECT count(*) FROM public.meeting_occurrences WHERE meeting_id = (SELECT id FROM owner_meeting)) < 2 THEN
    RAISE EXCEPTION 'room reuse collapsed occurrences';
  END IF;
END $$;

-- Lifecycle fields are RPC-only; a valid-looking direct archive must not bypass commit.
DO $$ BEGIN
  BEGIN
    UPDATE public.meeting_occurrences
      SET status = 'archived', consent_given_at = clock_timestamp(),
          staging_expires_at = NULL
      WHERE id = (SELECT id FROM connected_occurrence);
    RAISE EXCEPTION 'direct archive was allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    UPDATE public.meeting_occurrences SET user_id = '66666666-2222-4333-8444-555555555555'
      WHERE id = (SELECT id FROM connected_occurrence);
    RAISE EXCEPTION 'owner mutation was allowed';
  EXCEPTION WHEN check_violation OR insufficient_privilege THEN NULL; END;
END $$;

SET request.jwt.claim.sub = '66666666-2222-4333-8444-555555555555';
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM public.meeting_occurrences o JOIN connected_occurrence c ON o.id = c.id) THEN
    RAISE EXCEPTION 'foreign occurrence visible';
  END IF;
  IF (SELECT count(*) FROM public.activate_meeting_occurrence((SELECT id FROM connected_occurrence))) <> 0 THEN
    RAISE EXCEPTION 'foreign occurrence activation allowed';
  END IF;
END $$;

RESET ROLE;
SET ROLE anon;
DO $$ BEGIN
  BEGIN
    PERFORM * FROM public.meeting_occurrences;
    RAISE EXCEPTION 'anonymous occurrence read allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
  BEGIN
    PERFORM * FROM public.connect_meeting_occurrence(gen_random_uuid());
    RAISE EXCEPTION 'anonymous occurrence connect allowed';
  EXCEPTION WHEN insufficient_privilege THEN NULL; END;
END $$;
RESET ROLE;

SELECT 'meeting occurrence lifecycle and RLS checks passed' AS result;
