# 05 — Security, Operations & Validation

> **CURRENT — Execution instructions approved on 2026-10-24.** Security and bounded-resource requirements apply throughout implementation; this detail also defines final release gates.

---

## Goal

Define threat model, bounded queues/performance budgets, cost/quota safety, resilience patterns, observability, operator documentation, backup/restore, release checklist, and reproducibility — all tied to existing test commands and approved design constraints.

---

## Dependencies

- `01-architecture-data.md` through `04-documents-assistance.md`
- Master plan `../2026-10-24-execution-master.md`
- Approved PRD: `../../specs/2026-10-24-product-realignment-design.md` §10, §11
- Roadmap: S-01..S-12, L-01..L-06, M-01..M-15, N-01..N-06

---

## Existing Subsystems & Commands

| Area | Command | Status |
|---|---|---|
| Backend test | `cd backend && go test -race -count=1 ./...` | M-01 SELESAI LOKAL |
| Backend vet | `cd backend && go vet ./...` | M-02 SELESAI LOKAL |
| Extension test | `cd extension && npm test && npm run build` | M-03 SELESAI LOKAL (151/151) |
| Contract auth | `node --test --test-isolation=none http/auth.test.mjs` | M-04 SELESAI LOKAL (7/7) |
| Migration harness | `sh migration/tests/check-meetings.sh` | M-05 SELESAI LOKAL |
| Compose config | `docker compose config --quiet` | M-04 gate |
| Live Supabase | Manual Resterm workflow | M-08 PERLU VALIDASI |
| Live Deepgram | `docker compose run --rm backend -check deepgram` | M-09 SELESAI LOKAL |

---

## Proposed Contracts & Behavior

### 1. Threat Model Matrix (S-10)

| Asset | Threat | Mitigation | Residual |
|---|---|---|---|
| JWT (access/refresh) | Theft via XSS/log leak | `chrome.storage.session` + `TRUSTED_CONTEXTS`; no-store headers; short expiry; rotation | Low |
| Supabase keys | Leak in repo/log/build | Config rejects service-role; only publishable/anon in backend; secret never in extension | None |
| Audio frames | Intercept in transit | TLS 1.2+ all connections; backend→Deepgram TLS | Low |
| Transcript/Q&A | Cross-user access | RLS `user_id = auth.uid()` on all tables; owner checks in handlers | None |
| Documents | Prompt injection | Chunks as untrusted data; retrieval authorization deterministic; size limits | Low |
| Provider data | Operator access | Owner-only app ≠ provider guarantees; document in privacy policy | Accepted |
| Archive | Unauthorized restore | Signed URL short TTL; owner-only bucket; delete purges storage | Low |

**Principle:** Application owner-only does not extend to provider/infrastructure operators. Privacy policy must state this.

### 2. Bounded Queues & Performance Budgets (Desain §11)

| Queue | Scope | Capacity | Deadline | Backpressure |
|---|---|---|---|---|
| Audio upload outbox | Per run | 50 frames | 10s/request | Drop newer frame, preserve bounded memory |
| Transcript writer | Per run | 128 finals | 10s/request | Bounded writer failure; never unbounded growth |
| Translation | Per run | 128 finals | 15s/job | Enqueue nonblocking; overflow direkonsiliasi menjadi record terminal saat worker ditutup |
| Assistance (Q/A) | Per run | 64 finals | 15s/job | Enqueue nonblocking; final tanpa status direkonsiliasi menjadi kegagalan terminal saat worker ditutup |
| HTTP inflight | Backend | 64 requests | Handler-specific | Reject overload with 503 |

**Latency Targets (evaluation, not SLA):**
- Translation final: p95 ≤ 3s after STT final
- Q/A bilingual: p95 ≤ 8s after question final (doc ready)
- Report: p50/p95 with language, network, model, doc size, concurrency, sample count

### 3. Cost & Quota Safety (J-06, S-09, L-03)

| Control | P0 mechanism |
|---|---|
| Per-request cap | Bounded prompt/evidence/response bytes and provider timeout |
| Request cardinality | At most one translation and one assistance classification per persisted final; at most one answer repair |
| Degrade explicitly | Translation/provider failure remains a failed artifact; Q/A produces `-`; capture does not fabricate output |
| No billing retry | P0 performs no automatic paid-provider retry |
| No auto-pay | Startup/readiness never calls a paid provider; provider check is an explicit operator command |

Account-token accounting, 80% alerts, and per-user/provider 429 quota are P1 because the current provider contract does not expose trustworthy usage. Do not publish fabricated token/cost metrics.

### 4. Resilience Patterns

| Scenario | Behavior |
|---|---|
| Deepgram connection drop | Run status `failed`; capture stops; user sees error; no auto-reconnect |
| Sumopod timeout/quota | Q/A `provider_failed` / `quota_exceeded`; `-` surfaced; translation continues |
| SSE reconnect (extension) | Extension re-subscribes with `after_seq`; broker resends missed finals |
| Backend restart | In-flight request fails explicitly; no fabricated success. Automatic provider/session resume is outside P0 |
| Browser/service-worker restart | Persisted owner-local occurrence metadata remains; recovery behavior requires native Chrome validation |
| Provider outage | Translation/Q/A records explicit failure; `/ready` validates configuration only and never claims live dependency health |

### 5. Observability (L-01..L-04)

| Signal | Format | Fields |
|---|---|---|
| Structured log | metadata-only line | `trace_id`, method, path without query, status, duration; never content/secret |
| Health | `GET /health` | process liveness |
| Readiness | `GET /ready` | configuration validity without paid calls |
| Trace ID | `X-Trace-ID` | generated per backend request |

Prometheus token/cost/queue metrics and end-to-end provider trace propagation are P1/P2 roadmap work, not P0 claims.
**No content in logs/metrics:** Never log audio bytes, transcript text, document text, Q/A, tokens, JWT, connection strings.

### 6. Operator Documentation (L-05)

| Doc | Content |
|---|---|
| Deploy | Compose up, env vars, Supabase project setup, migrations apply, Cron enable |
| Migrate | Migration order, backfill, rollback steps, zero-downtime where possible |
| Rotate keys | Supabase keys, Deepgram key, Sumopod key — procedure without downtime |
| Restore | DB point-in-time, private bucket restore, archive manifest verification |
| Capacity | Bucket size monitoring, DB size, pg_cron job health, queue depth alerts |

### 7. Backup & Restore (L-06, 02 Archive)

| Object | Method | RPO/RTO |
|---|---|---|
| PostgreSQL (Supabase) | PITR + daily snapshot | RPO 5m / RTO 30m |
| Private buckets (staging/archives) | Supabase storage replication + manual export | RPO 1h / RTO 1h |
| Archive manifests | Stored in DB + replicated | RPO 5m / RTO 30m |
| **Archive delete** | Soft delete (status `deleted`); storage purge async; restore via manifest within vendor backup window | — |

**No automatic TTL on archives.** Capacity alert at 80% bucket quota.

### 8. Release Checklist (N-01..N-06)

- [ ] `docker compose config --quiet` passes
- [ ] `go test -race ./...` + `go vet ./...` pass
- [ ] `npm test` + `npm run build` pass (extension)
- [ ] Contract auth 7/7 passes
- [ ] Migration harness passes (0001–0011 idempotent)
- [ ] No secrets in `extension/dist`, Docker image, logs
- [ ] Manifest V3 valid; permissions minimal
- [ ] TLS certs valid for deployment domain
- [ ] Rollback: previous extension version packaged; DB migration reversible (drop new tables)
- [ ] Privacy policy updated (provider data processing, no team sharing)
- [ ] Operator docs complete (deploy, migrate, rotate, restore, capacity)

### 9. Reproducibility

- All verifications use existing commands (see table §Existing Subsystems)
- No new test infrastructure required
- Deterministic builds: `go build -trimpath`, `npm ci` lockfile
- Pin provider SDK versions in `go.mod` / `package-lock.json`

---

## Task Instructions (Checkbox)

- [x] Document threat model matrix with mitigations
- [x] Specify bounded queue capacities, deadlines, and backpressure per pipeline
- [x] Define latency targets as evaluation criteria (not SLA)
- [x] Separate implemented request/cost bounds from deferred provider quota accounting
- [x] Enumerate P0 resilience behavior without claiming automatic resume
- [x] Define metadata-only logs, health/readiness, and trace ID; defer metrics accurately
- [x] Write operator docs for deploy, cleanup, rotate, restore, capacity, incident, rollback
- [x] Define backup/restore RPO/RTO and external drill gate
- [x] Write release checklist with all local and external gates
- [x] Confirm reproducibility through repository commands
- [x] Map S/L/M/N roadmap features to this plan

---

## Acceptance & Failure/Race Checks

- Structured logs contain no transcript/Q&A/document/audio/token/JWT/connection string
- Health/readiness report liveness and configuration validity separately
- Every queue and parser/container resource is bounded; backend inflight overload returns 503
- Deepgram terminal drop → run failed, capture stops, no implicit replay
- Sumopod failure → explicit failed translation or Q/A `-`; capture remains independent
- Backup/restore runbook requires a database+private-object consistency set and manifest verification; production drill remains an external gate
- Release gates pass before private distribution
- No new test commands introduced

---

## Migration & Cutover

- Observability endpoints added to `internal/httpapi`
- Metrics middleware wrapped on all routes
- Structured logger replaces any `log.Printf` in backend
- pg_cron job 0011 for staging TTL (replaces 0005 for new data)
- Release artifacts: extension zip, Docker image tag, migration SQL bundle

---

## Non-Goals

- No distributed tracing infrastructure (Jaeger/Zipkin) — trace ID correlation only
- No auto-scaling / Kubernetes
- No Redis / external queue
- No vector DB service
- No team sharing features
- No summary/action items (H-08/H-09)
- No speaker diarization (DEC-06)
- No joined-state detection (DEC-02)
- No provider redundancy (DEC-17)