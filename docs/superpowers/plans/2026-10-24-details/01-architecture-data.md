# 01 — Architecture & Data Contracts

> **CURRENT — Execution instructions approved on 2026-10-24.** Implement incrementally against the repository baseline; preserve existing verified subsystems and update roadmap evidence after each package.

---

## Goal

Define the modular Go backend architecture, Supabase Auth/Data API contracts, private team provisioning, and the complete logical schema with migration cutover from legacy implementations — without writing implementation code.

---

## Dependencies

- Approved PRD/design: `../../specs/2026-10-24-product-realignment-design.md` (DISETUJUI)
- Master plan: `../2026-10-24-execution-master.md`
- Existing baseline: F0 runtime (A-01..A-12), F1 auth (B-01..B-15), F2 meeting URL (C-01..C-20)

---

## Existing Subsystems (to be discovered via find)

- `backend/cmd/server/main.go` — entry point, route registration
- `backend/internal/config/config.go` — env validation, reject secret/service-role
- `backend/internal/auth/` — handler, service, repository, model (Supabase HTTP API)
- `backend/internal/meeting/` — handler, service, repository, model (resolve/list/detail)
- `backend/internal/audio/` — run/chunk/stop handlers, writer, broker
- `backend/internal/transcribe/` — Deepgram streaming, SSE
- `backend/internal/deepgram/`, `backend/internal/sumopod/` — provider checks
- `migration/0001_supabase_data_api.sql`, `0002_meeting_sessions.sql`, `0003_meeting_runs.sql`, `0004_transcript_segments.sql`, `0005_transcript_retention_cron.sql`
- `compose.yaml` — single backend service
- `extension/src/` — React MV3 popup, background SW, offscreen, config

---

## Proposed Modules & Contracts

### 1. Modul Go (satu binary, internal packages)

| Package | Responsibility | Exposed to |
|---|---|---|
| `cmd/server` | Wiring, graceful shutdown, config load | — |
| `internal/config` | Env parsing, validation, typed Config struct | All |
| `internal/httpapi` | Middleware (auth, rate limit, no-store, timeouts, error mapping), router | `cmd/server` |
| `internal/auth` | Register, login, refresh, logout, me, private team provisioning | `internal/httpapi` routes |
| `internal/meeting` | Room resolve, list, detail, occurrence CRUD | `internal/httpapi` routes |
| `internal/occurrence` | Occurrence lifecycle, document association, consent, staging, archive | meeting, audio, document, assistance, archive |
| `internal/audio` | Run create, chunk upload, stop, writer final segments | `internal/httpapi` routes, occurrence |
| `internal/transcribe` | Deepgram streaming client, SSE broker, segment seq/timestamp | audio |
| `internal/translate` | Segment → Indonesia, revision, source link, failure status | occurrence (after transcribe final) |
| `internal/document` | Upload, validate, parse (per format), version, extraction, locator | occurrence, assistance |
| `internal/retrieval` | Owner+occurrence scoped search, citation builder | assistance |
| `internal/assistance` | Question detection, mapping, bilingual draft, citation check, revision | occurrence, document, retrieval |
| `internal/archive` | Consent, manifest, commit, list, read, delete, export, restore | occurrence, assistance, document |
| `internal/provider/deepgram` | Streaming client, prerecorded check, config | transcribe |
| `internal/provider/sumopod` | Chat completions (non-stream check, stream prod), config | translate, assistance |

**Coupling rules:** No circular imports. Provider adapters do not set product policy. DTOs in `internal/shared/dto`. Cancellation/deadline via `context.Context` per request/job.

### 2. Supabase Auth & Data API Contracts

| Flow | Endpoint (Go) | Supabase Call | RLS / Ownership |
|---|---|---|---|
| Register | `POST /api/auth/register` | `POST /auth/v1/signup` + profile trigger | Anon key; profile created via trigger |
| Login | `POST /api/auth/login` | `POST /auth/v1/token?grant_type=password` | Anon key; returns session |
| Refresh | `POST /api/auth/refresh` | `POST /auth/v1/token?grant_type=refresh_token` | Rotates; single-flight |
| Logout | `POST /api/auth/logout` | `POST /auth/v1/logout?scope=local` | Bearer JWT |
| Me | `GET /api/auth/me` | `GET /auth/v1/user` → `GET /rest/v1/profiles?id=eq.user_id` | Bearer JWT, RLS owner |
| Team provisioning | `POST /api/admin/team/invite` (admin only) | Create user via Admin API, set `app_metadata.role=member` | Service role only in admin binary (not in main backend) |

**Error codes (from B-08):** `supabase_config`, `auth_api_error`, `profile_api_error`, `profile_missing`, `unauthorized`, `timeout`. All auth responses: `Cache-Control: no-store`.

### 3. Logical Schema (owner-only RLS on every table)

| Table | Key Columns | Notes |
|---|---|---|
| `profiles` | `id` (FK auth.users), `display_name`, `created_at` | Existing 0001 |
| `meeting_sessions` | `id`, `user_id`, `meeting_key`, `canonical_url`, `provider`, `provider_host`, `provider_room_id`, `created_at` | Existing 0002; `UNIQUE(user_id, meeting_key)` |
| `meeting_occurrences` | `id`, `user_id`, `meeting_session_id` (FK), `started_at`, `ended_at`, `status` (pending_documents/active/awaiting_decision/archived/discarded), `documents_uploaded` (bool), `consent_given_at` (nullable), `staging_expires_at` (nullable) | NEW 0006 |
| `meeting_runs` | `id`, `user_id`, `occurrence_id` (FK), `meeting_session_id` (FK), `status`, `bytes`, `chunks`, `started_at`, `ended_at`, `sample_rate`, `save_transcript` (legacy, immutable false) | Existing 0003 |
| `transcript_segments` | `id`, `user_id`, `run_id` (FK), `occurrence_id` (FK), `seq`, `text`, `start`, `duration`, `language`, `created_at` | Existing 0004; `UNIQUE(run_id, seq)` |
| `translations` | `id`, `user_id`, `segment_id` (FK), `language` ('id'), `text`, `source_revision`, `status`, `created_at` | NEW 0007; `UNIQUE(segment_id, language)` |
| `documents` | `id`, `user_id`, `occurrence_id` (FK), `version`, `filename`, `mime_type`, `size_bytes`, `storage_path`, `status` (uploading/ready/failed), `extracted_at`, `extraction_quality`, `created_at` | NEW 0008 |
| `document_chunks` | `id`, `user_id`, `document_id` (FK), `occurrence_id` (FK), `section_locator`, `text`, `chunk_index`, `token_count`, `created_at` | NEW 0008; retrieval source |
| `questions` | `id`, `user_id`, `occurrence_id` (FK), `run_id` (FK), `question_id_stable`, `source_segment_ids`, `text`, `display_order`, `version`, `status`, `created_at` | NEW 0009 |
| `answers` | `id`, `user_id`, `question_id` (FK), `occurrence_id` (FK), `language` ('id'/'en'), `text`, `citations` (JSON: locator+verbatim), `revision`, `evidence_status` (supported/none/parse_failed/retrieval_failed/provider_failed/quota_exceeded), `created_at` | NEW 0009 |
| `archives` | `id`, `user_id`, `occurrence_id` (FK), `manifest` (JSON: all object refs + hashes), `status` (committed/partial_failed), `committed_at` | NEW 0010 |

All new tables: `id` UUID PK, `user_id` FK `auth.users`, timestamps, appropriate unique constraints, minimum grants, `SECURITY INVOKER` RPCs, RLS `user_id = auth.uid()`.

### 4. Migration Cutover Plan

| Step | Migration | Action | Rollback |
|---|---|---|---|
| 1 | 0001, 0002, 0003 | Keep existing (already applied) | — |
| 2 | 0004, 0005 | Keep transcript_segments + pg_cron purge (historical 30-day) | — |
| 3 | 0006 | Create `meeting_occurrences`; backfill from `meeting_runs` grouped by user+meeting_session+date bucket | Drop table |
| 4 | 0007 | Create `translations`; no backfill (new feature) | Drop table |
| 5 | 0008 | Create `documents`, `document_chunks`; no backfill | Drop tables |
| 6 | 0009 | Create `questions`, `answers`; no backfill | Drop tables |
| 7 | 0010 | Create `archives`; no backfill | Drop table |
| 8 | 0011 | Purge policy: disable 0005 pg_cron job for transcript_segments; new job cleans `staging` objects (occurrence `staging_expires_at` passed, status ≠ archived) only | Re-enable 0005 job |
| 9 | Code cutover | Update all handlers to read/write new tables; legacy `save_transcript` ignored; migration is one-way for new features | Git revert |

**Compatibility:** Existing `transcript_segments` with 30-day purge continues for old runs. New runs under occurrences use staging→archive model. No data loss. Migration idempotent (IF NOT EXISTS, CREATE INDEX CONCURRENTLY where applicable).

---

## Task Instructions (Checkbox)

- [ ] Document Go module boundaries, router wiring, middleware stack
- [ ] Specify Config struct fields with validation rules (reject secret, require model names)
- [ ] Define Auth HTTP contracts (request/response JSON, error codes, rate limit)
- [ ] Define Meeting/Occurrence/Run HTTP contracts
- [ ] Define Document/Chunk/Question/Answer/Archive HTTP contracts
- [ ] Write SQL migration files 0006–0011 with RLS, grants, RPCs
- [ ] Write backfill script for 0006 occurrences from existing runs
- [ ] Define purge policy migration 0011 (staging TTL only, not archive)
- [ ] Add historical banners to `docs/superpowers/plans/2026-10-18-agent-initialization.md`, `2026-10-18-auth-implementation.md`, `specs/2026-10-18-auth-design.md`
- [ ] Verify migration order and rollback steps documented

---

## Acceptance & Failure/Race Checks

- Migration 0006–0011 apply clean on empty DB and on DB with 0001–0005
- Backfill 0006 produces one occurrence per user+meeting_session+calendar day
- RLS blocks cross-user SELECT/INSERT/UPDATE/DELETE on all new tables
- RPCs execute as invoker with `search_path` fixed
- No pgx or direct DB connection in backend codebase (grep for `pgx`, `sql.Open`)
- Auth error codes match B-08 contract
- All auth responses have `Cache-Control: no-store`
- Team provisioning uses separate admin binary, not main backend

---

## Migration & Cutover

See table in §4. Legacy `save_transcript` on runs stays immutable false. New consent lives on `meeting_occurrences.consent_given_at`. Staging cleanup via 0011 job. Archive never purged by age.

---

## Non-Goals

- No public signup, no team shared data, no admin dashboard UI
- No Redis, no external queue, no vector DB service
- No changes to existing 0001–0005 behavior or data
- No provider model selection in this task (gated research in 03, 04)