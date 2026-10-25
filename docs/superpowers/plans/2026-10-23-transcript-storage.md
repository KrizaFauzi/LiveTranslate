# F6 Transcript Storage Implementation Plan

> **HISTORICAL — Superseded by 2026-10-24 execution planning (02-lifecycle-archive.md).** This plan describes opt-in final transcript storage with 30-day retention and pg_cron purge. Current design: post-meeting consent, permanent archives, staging TTL 24h. See `docs/superpowers/plans/2026-10-24-details/02-lifecycle-archive.md` for current design. Contents preserved for history; do not execute.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement opt-in final transcript archives per capture, with 30-day retention, popup timeline, terminal delete and complete JSON export.

**Architecture:** Final segments enter a bounded server-owned writer directly at session publication, independently of lossy SSE subscribers. Supabase Data API/RLS stores owner/run-bound immutable finals and serializes writes/deletion; pg_cron physically purges expired text. Popup accesses saved data only through the authenticated worker, keeping live state separate.

**Tech Stack:** Existing Go standard library, Supabase PostgreSQL/Data API/Auth, React JavaScript/Vite MV3, Node built-in tests, Go httptest, isolated PostgreSQL harness.

**Spec:** `docs/superpowers/specs/2026-10-23-transcript-storage-design.md` (approved by user 2026-10-23).

## Global Constraints

- Per-run opt-in default false, final transcript only; no audio, partials, translation or AI persistence.
- Retention: 30 days from database run.started_at; no extension of expiry by update or stop.
- No Go/npm runtime dependency or Compose service additions, backend refresh/service-role tokens. App RPCs use SECURITY INVOKER and owner RLS. Supabase Cron is an explicit operational prerequisite; scheduled purge uses database admin only, outside application credentials.
- Writer queue: 128 finals or 2 MiB queued text, 10-second request deadline, 30-second finalization/drain budget. Saving failure leaves live capture/transcription running but archive incomplete; no automatic retry/outbox.
- Changes to exported Go/JS symbols require LSP references before mutation; clean cutover all callers, no compatibility shims.
- Each implementation owner writes behavioral tests first; parent observes failing feature regressions before product edits. Workers skip build/lint/tests/formatters mid-flight; parent runs the checks centrally after integration.
- Permanent tests cover behavior/invariants, not exact source text, wiring, copies, mock echoes or incidental defaults.

## Review Focus

1. A direct Data API DELETE or run-state PATCH must not bypass terminal-delete/purge coupling; SQL tests include direct mutation, repeat delete and append after delete.
2. A write that waited on a parent lock across expiry must recheck clock_timestamp; SQL checks cover post-lock expiry, stale consent and parent-owner mismatch.
3. Early final results, expired/refreshed JWTs, stop finalization and provider termination must not outlive writer ownership or falsely report saved; backend tests cover those transitions and drain failure.
4. Saved history/export beyond 200 finals must be complete and snapshot-bound, even during capture; backend and frontend tests cover page boundaries, empty snapshots and explicit incomplete status.
5. Logout/account/selection races must not display or download another context; frontend regression tests invalidate pending reads/export before publication/download.

## Ownership and shared contracts

Three independent implementation slices share the interfaces below. One integration owner (parent) owns plan/spec/roadmap/status/README, parent regression file, final verification and conflict resolution. Slice owners must not modify each other's files.

### Database contract

`meeting_runs` additions: `save_transcript boolean`, `transcript_state text` (`disabled|saving|saved|failed|deleted`), `transcript_expires_at timestamptz` nullable. Consent and expiry immutable; old runs disabled. `expired` is derived.

`transcript_segments`: `id,user_id,meeting_id,run_id,seq,text,start,duration,language,created_at`, unique `(run_id,seq)`; normal JSON names. Parent/owner consistency enforced database-side; finals immutable. Seq retains partial gaps.

Data API RPCs (SQL parameter names are the JSON body names):

- `append_transcript_final(run_uuid uuid, segment_seq integer, segment_text text, segment_start double precision, segment_duration double precision, segment_language text) RETURNS SETOF public.transcript_segments`. Return one row for accepted or identical duplicate; zero rows for disabled/deleted/expired; conflicting existing seq is rejected. Validate parent and lock before checking expiry/inserting. No transcript text in errors.
- `delete_transcript(run_uuid uuid) RETURNS SETOF public.meeting_runs`: owner row with terminal state deleted and all text physically removed; idempotent; zero rows for missing/foreign run. Works for empty archives. Direct exposed DELETE/PATCH gets same terminal-delete invariant or is rejected.
- `set_transcript_state(run_uuid uuid, state_value text) RETURNS SETOF public.meeting_runs`: only allowed nonterminal state transitions to saved/failed; must not revive deleted/expired/disabled. Zero rows on terminal/disallowed/no-owned run; callers must not interpret that as saved.
- `purge_expired_transcripts() RETURNS bigint`: admin maintenance function with no EXECUTE for PUBLIC/anon/authenticated, physically deletes expired segments; preserves meeting/run metadata. Tests can invoke as admin. Scheduler job `livetranslate-transcript-retention`, `* * * * *`, invokes it once per minute. Idempotent operational migration.

Authenticated grants required by invoker RPCs must be guarded on direct Data API writes too. Do not silently switch to owner-bypassing SECURITY DEFINER functions. If invariants cannot be realized with these grants, message parent before changing contract.

### HTTP and frontend contract

Run JSON includes the three database fields above. Storage availability is derived as expired if current DB expiry passed, incomplete if saving without live writer, otherwise state. Responses carry `storage` metadata `{state, expires_at, complete}` separate from `run.status`.

- Create: existing `POST /api/audio/runs` body adds optional boolean `save_transcript`, missing=false, null/nonboolean/unknown fields rejected. Response `{run}` remains; run includes storage fields.
- List: `GET /api/audio/runs?meeting_id=<uuid>&limit=20&cursor=<opaque>` -> `{runs:[Run...], next_cursor:null|string}`. Default 20, max 100. Opt-in/nondeleted/unexpired runs only, stable `(started_at DESC,id DESC)`.
- Read: `GET /api/audio/runs/{id}/segments?after_seq=0&through_seq=<snapshot>&limit=50` -> `{run, storage:{state,expires_at,complete}, segments:[Segment...], through_seq:int, next_after_seq:null|int}`. Default 50/max100. Initial omitted through_seq freezes current saved max seq, including 0. Optional meeting_id query verifies run association if supplied. Unavailable owner archives return empty segments and explicit disabled/deleted/expired state, not other users' data.
- Delete: `DELETE /api/audio/runs/{id}/segments` -> `{run, storage:{state,expires_at,complete:false}}`; same owner idempotent. Optional meeting_id query verifies association.
- Foreign/missing/invalid UUID -> 404; invalid pagination -> 400; all JSON no-store. No text/provider details in errors.
- No separate export endpoint: popup obtains all snapshot pages via worker and creates JSON/Blob once complete. Use spec schema_version=1 and metadata; no partial successful file.
- Worker RPC names: `transcript.runs.list`, `transcript.run.read`, `transcript.run.delete`, carrying spec input fields. Generic existing extensionRequest remains transport. Popup never receives tokens.
- Storage errors are observable via saved read/list and live controller metadata when available, not injected as transcript provider failure. Poll active opted-in archive only while relevant popup view is open; do not add a permanent browser alarm/provider request for this.

### Backend sink contract

Add `transcribe.FinalSink` with `Enqueue(Segment)` and `Finish()`; implementations are nonblocking in-memory operations, never database I/O. `Broker.Open(ctx, runID, sampleRate, sink)` and `NewSession(stream,cancel,sink)` receive it before supervision. Nil for opt-out/tests. Each accepted final is offered once with canonical seq, including with no SSE subscriber; Finish signals no more results exactly once. Migrate all callers/tests. Reject duplicate live run opening rather than resetting seq within a persisted run.

Audio owns writer/controller map, current verified user token, monotonic request tickets, repository calls, drain and terminal state. Provider interfaces/config remain unchanged. `NewHandler`/`RegisterRoutes` signatures stay stable.

## Task 1: Transactional schema, RLS, deletion and retention

**Files:** Create `migration/0004_transcript_segments.sql`, `migration/0005_transcript_retention_cron.sql`, `migration/tests/transcript_segments.sql`; modify `migration/tests/run_all.sql` and only necessary harness support. Do not edit other docs (parent owns them).

**Interfaces:** Produces database/RPC contract above; consumes existing 0001–0003, auth.uid and roles from harness.

- [x] Write SQL regressions before migration: opt-out inserts rejected; final duplicate identical retains one row; conflicting same-seq text rejected; owner/meeting mismatch and cross-account calls blocked; anon denied. Expected values are literal rows/content, not copies of schema text.
- [x] Add state/expiry/delete cases: delete empty/saved/active archives, direct DELETE/PATCH guard, future insert blocked, expired rows unreadable and purged, unexpired text/run/counters unchanged, consent/expiry immutable. Test reapply without losing saved data. Controlled concurrency harness must prove deletion/append and post-lock expiry boundary, not just independent transactions.
- [x] Implement schema and RPC contract with shared parent lock; direct mutation guards must enforce the same invariant as RPC. RLS uses current wall-clock time where relevant. Metadata remains accessible to owner after text removal.
- [x] Implement admin purge function and separate pg_cron install/job migration, failing clearly if scheduler unavailable. Reapply upserts named job. Keep ordinary no-network PostgreSQL schema harness usable without pg_cron.
- [x] Parent runs SQL harness centrally after integration; real pg_cron scheduling requires supported PostgreSQL runtime or report external activation pending. Do not apply cloud migrations automatically.

## Task 2: Server-generated final persistence and archive API

**Files:** Modify `backend/internal/audio/{handler,repository,model,sse}.go` only where needed; add focused `archive.go`, `archive_repository.go`, `transcript_writer.go` and corresponding tests. Modify `backend/internal/transcribe/{session,broker}.go` plus impacted tests/callers. Preserve all pre-existing changes. Parent owns `backend/internal/audio/f6_regression_test.go` if created.

**Interfaces:** Consumes exact SQL/RPC contract. Produces exact HTTP and FinalSink contracts above. No frontend edits or doc edits.

- [x] Add failing Go behavior tests: consent false causes zero text writes; true stores finals without subscriber and beyond backlog; canonical seq gaps and immutable duplicate behavior; no partial persistence; sink attached before early final.
- [x] Add writer transitions: overflow/count/bytes, DB failure/timeout, stale-vs-refreshed credentials, stop-before-final + drain, provider failure, deleted/expired skipped writes, restart saving interpreted incomplete; never falsely saved. Use injected provider/Data API/clock, no paid calls or sleeps for synchronization.
- [x] LSP references before signature/model changes. Implement FinalSink integration at publication, lifecycle Finish on both stop/provider end, duplicate-open guard, clean-cutover all callers.
- [x] Implement bounded writer and verified credential refresh from create/chunk/stop, owner lifetime through 30-second cleanup, no network under session mutex, no retry/outbox. Preserve F4 stop behavior and transcript error semantics.
- [x] Implement repository and APIs with stable pagination/snapshot, expired/deleted/disabled availability, no-store/error contracts, and read/export-independent-of-broker. Validate returned identities; cap responses explicitly, never truncate.
- [x] Add route behavior tests for >200 finals/multiple pages, zero snapshot, invalid/foreign IDs, list pagination, deletion barrier, expired/failed archives. Extend local fake Data API meaningfully for persistence, not source-text or wiring tests.
- [x] Parent runs go test -race/vet/build centrally after all slices complete. Report exact files/signatures and unverified risks.

## Task 3: Per-run consent, saved timeline, export and account isolation

**Files:** Modify `extension/src/{App.jsx,background-runtime.js,audio-capture.js,offscreen.js,transcript-state.js,style.css}` only as needed; add `saved-transcripts.js` (worker/client paging state helpers) and focused behavioral tests. Parent owns `extension/src/f6-regression.test.js` if created. No backend/SQL/docs edits.

**Interfaces:** Consumes exact HTTP/RPC contract. Existing `audio.start` input becomes `{save_transcript:boolean}`; meeting/tab still worker-owned. Live/offscreen messages must include owner/run identity as needed; migrate every producer/consumer without aliases.

- [x] Add failing behavioral tests for opt-in reaching create request and immutable server-confirmed run choice; default/omitted produces no permanent storage; reopening/next run resets consent. Test real state/requests, not checkbox source text.
- [x] Implement checkbox/disclosure and server-authoritative running storage choice. Snapshot/reset next-start choice; storage disabled is the default and live transcript works unchanged.
- [x] Implement owner-scoped worker list/read/delete RPCs, authenticated same-account refresh and generation checks. Do not expose tokens. Bind live capture/transcript/offscreen progress/error/token state to owner+run and clear on logout/account transitions; preserve final-flush-before-token-removal ordering.
- [x] Add saved run selector, timed paginated timeline, availability/incomplete/empty/loading/errors, terminal delete without stopping capture, and full snapshot JSON download. Keep live and saved selection separate.
- [x] Tests cover >200 final complete export/multipage, through_seq=0, account/meeting/selection response races, delete/expiry mid-export, no download on incomplete fetch, no stale owner content. Extract helpers only where they implement reusable behavior; no generic framework.
- [x] Parent runs extension suite/build and real browser smoke centrally. No new runtime dependency/downloads permission without demonstrated need.

## Task 4: Integrate, smoke, review and evidence

**Owner:** Parent. Files: README, migration/README, F6 status doc, approved spec/plan/roadmap/pending queue; throwaway smoke fixtures outside repo or removed after use.

- [x] Before product changes, run focused failing regression(s) for missing F6 consent/archive behavior and observe correct failure. Coordinate no product edits before this evidence.
- [x] Integrate three slices against pinned contracts; resolve root causes, not compatibility shims or mocked no-op storage.
- [x] Run centrally: `go test -race -count=1 ./...`, `go vet ./...`, Go build; `npm test`, `npm run build`; auth contract tests; isolated SQL harness; `docker compose config --quiet`.
- [x] Run actual backend smoke with controlled local provider/Data API and real PostgreSQL where reachable: run opt-in -> final -> read pages -> export data -> delete -> no resurrection; expiry/purge removes text while preserving metadata. No paid requests or cloud mutation without explicit operator action.
- [x] Open actual popup/browser surface with managed Chromium if possible; exercise consent, archive timeline and JSON download against controlled backend. Report real capture/provider and live pg_cron limitations separately, never mark them passed from fakes.
- [x] Request independent reviews for data/lifecycle and frontend/account boundaries, apply verified consequential findings, then rerun affected verification.
- [x] Update roadmap with exact exercised evidence and manual/external boundaries, migration activation/rollback instructions, README consent/retention/export behavior, and F6 status. Remove throwaway scaffolds after smoke.
- [x] Sync docs to Obsidian with read/precondition/read-back/baseline only if connection recovers; otherwise update existing pending entries without duplicate writes or claiming synchronization.

## Self-review

User decisions and spec sections1–9 map to Tasks1–3. All ten acceptance items map to SQL invariants, server/writer/API regressions, popup/export/races and smoke evidence. Shared contracts stayed fixed; no compatibility shims or cloud/paid mutations were added.

## Execution outcome — 2026-10-24 local

All implementation slices, parent verification and scoped fix reviews completed in the existing dirty workspace without commits/resets. LSP had no configured server; known-symbol text-reference fallback was recorded before signature cutover. Full evidence, review regressions, activation steps and external boundaries: [F6 status](../../implementation/transcript-storage-status.md).

Local Go race/vet/build, extension147/147+build, auth7/7, Compose, SQL/RLS/concurrency, actual pg_cron purge, missing-scheduler rejection, real Go/PostgREST/PG API206final/5pages and controlled popup/JSON download passed. The final backend binary's HTTP smoke passed again after the last review fix. Owned smoke services/tab/container/image and throwaway source/scripts/binaries were removed; recovery/review ledger retained under ignored `.superpowers`.

F6 remains **PERLU VALIDASI**, not production-complete: target Supabase0004/0005 and unpacked/native Chrome/provider path were not applied/exercised. Obsidian REST was unreachable after retry; pending queue updated, no vault write/read-back or baseline update claimed.
