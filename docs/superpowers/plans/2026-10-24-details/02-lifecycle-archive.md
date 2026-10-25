# 02 — Lifecycle, Consent & Archive

> **CURRENT — Execution instructions approved on 2026-10-24.** This replaces the earlier pre-capture consent and 30-day retention design.

---

## Goal

Define occurrence lifecycle, post-meeting consent flow, staging vs archive separation, commit/discard/expiry races, restart/credential lifecycle, and private object cleanup — all with migration cutover from legacy 30-day transcript retention.

---

## Dependencies

- `01-architecture-data.md` (schema, migrations, auth)
- Master plan `../2026-10-24-execution-master.md`
- Approved PRD: `../../specs/2026-10-24-product-realignment-design.md` §6, PRD-11..PRD-13

---

## Existing Subsystems

- `backend/internal/audio/` — run create/chunk/stop, writer final segments
- `backend/internal/transcribe/` — segment broker, SSE
- `migration/0003_meeting_runs.sql`, `0004_transcript_segments.sql`, `0005_transcript_retention_cron.sql`
- `extension/src/audio-capture.js`, `offscreen.js`, `background-runtime.js`
- `extension/src/App.jsx` — popup capture controls, timeline

---

## Proposed Lifecycle & Contracts

### 1. Occurrence State Machine

```
pending_documents  →  active  →  awaiting_decision (24h TTL)  →  archived
                        ↓              ↓ discard/no-decision        ↓
                     (capture)       discarded                     (permanent)
```

- **pending_documents**: Occurrence created (via meeting resolve or doc upload). User can upload documents. No capture yet.
- **active**: At least one run started. Audio streaming, transcription, translation flowing. Documents locked for this occurrence (new uploads create new version).
- **awaiting_decision**: Triggered by explicit end (user stops capture, tab closes, logout, transcription terminal error, or occurrence 4h cap reached). Staging window opens. `staging_expires_at = now() + 24h`.
- **archived**: User clicks "Simpan" → commit manifest (idempotent). All staging objects promoted to archive. `consent_given_at` set. Status permanent.
- **discarded**: User clicks "Tidak" OR 24h expiry without decision → barrier rejects late jobs → purge staging objects (transcript, translation, documents, chunks, questions, answers). `meeting_runs` metadata retained. Occurrence status `discarded`.

### 2. Consent & Commit

- **Trigger**: After occurrence moves to `awaiting_decision`, popup shows "Simpan hasil meeting?" with preview of what will be saved (transcript segments count, translation segments, Q/A count, document list, estimated size).
- **Simpan**: POST `/api/archives/commit` `{occurrence_id}`. Idempotent: repeat calls return same manifest. Steps:
  1. Verify occurrence status `awaiting_decision` and owner.
  2. Collect all staging objects (transcript, translations, Q/A, documents, chunks).
  3. Write manifest JSON with object refs + SHA256.
  4. Insert `archives` row with manifest, status `committed`.
  5. Update `meeting_occurrences` → `archived`, `consent_given_at = now()`.
  6. Return manifest to UI for confirmation.
- **Tidak / Expiry**: 
  - POST `/api/archives/discard` `{occurrence_id}` OR automatic at `staging_expires_at` via cron (0011).
  - Barrier: set occurrence status `discarding` (lock). Reject any late writer jobs.
  - Delete staging: transcript segments, translations, questions, answers, documents, chunks for this occurrence.
  - Keep `meeting_runs`, `meeting_sessions`, `meeting_occurrences` (status `discarded`).
  - No audio ever stored.

### 3. Staging Storage

- **Location**: Supabase private bucket `staging/{user_id}/{occurrence_id}/` (or DB tables with `occurrence_id` FK and status column).
- **Objects**: transcript segments, translations, Q/A drafts, documents, document chunks, extraction artifacts.
- **Access**: Signed URL (short TTL) for popup display. Owner-only via RLS.
- **No audio**: Raw audio never written to storage.

### 4. Race Conditions & Handling

| Race | Resolution |
|---|---|
| Save commit vs late transcription final | Writer checks occurrence status before insert; if not `active`/`awaiting_decision`, drop |
| Save commit vs credential expiry | Commit runs with fresh JWT from user action; if 401, surface error, allow retry |
| Discard (user) vs late writer job | Discard sets status `discarding` under lock; late jobs see status and drop |
| Discard (auto 24h) vs user clicking Simpan | Simpan checks `staging_expires_at`; if not expired, proceed; if expired, reject |
| Browser crash during active | Occurrence stays `active`; on reload, SW detects existing occurrence, resumes; 4h cap enforced server-side |
| Backend restart during awaiting_decision | Staging objects persist; cron job cleans only expired (`staging_expires_at < now()`) |
| Derived work still active | Commit returns unavailable until every final has translation + assistance status and every document is terminal |
| Terminal artifact failure | Commit succeeds as `partial_failed`; export preserves metadata and marks a genuinely unavailable failed source explicitly |

### 5. Archive Manifest Schema

```json
{
  "schema_version": 2,
  "occurrence_id": "uuid",
  "objects": [
    {"type": "transcript_segments", "count": 206, "sha256": "..."},
    {"type": "translations", "count": 206, "sha256": "..."},
    {"type": "documents", "count": 3, "sha256": "..."},
    {"type": "document_chunks", "count": 45, "sha256": "..."},
    {"type": "questions", "count": 12, "sha256": "..."},
    {"type": "answers", "count": 12, "sha256": "..."},
    {"type": "assistance_segment_status", "count": 206, "sha256": "..."}
  ]
}
```

Status `committed` atau `partial_failed`, waktu commit, dan delete tombstone berada pada row archive, bukan di dalam payload manifest. Export memanggil verifikasi hash/count database sebelum menulis ZIP.

### 6. History, Read, Export, Delete

- `GET /api/archives?limit=20&cursor=` — list keyset owner-only; cursor terikat owner
- `GET /api/archives/{id}` — manifest + metadata dokumen
- `GET /api/archives/{id}/export` — stream ZIP: seluruh tabel JSON + file sumber yang tersedia; verifikasi manifest dan SHA-256
- `DELETE /api/archives/{id}` — soft-delete archive, pertahankan manifest/tombstone, purge metadata artefak, antrekan hapus object idempoten
- Restore hanya melalui drill operator consistency-set/PITR dalam backup window. **Tidak ada endpoint restore pengguna:** manifest tidak menyimpan konten yang sudah dihapus.

### 7. Private Object Cleanup

- Semua object memakai private bucket `meeting-documents` dengan path `{user_id}/{occurrence_id}/{document_id}/{file_name}`.
- Discard, expiry, dan archive delete mencatat path ke `storage_cleanup_jobs`, lalu menghapus metadata dalam transaksi.
- Backend mencoba Storage API dengan JWT owner; worker admin satu-kali mengulang antrean gagal memakai secret terisolasi. Tidak ada SQL langsung terhadap `storage.objects`.
- Scheduler staging tiap 15 menit hanya memanggil purge occurrence expired; scheduler host menjalankan worker object cleanup setidaknya tiap 15 menit.
- Account deletion wajib memakai alur admin yang turut mengantrikan seluruh object sebelum FK cascade.

### 8. Restart / Credential Lifecycle

- Writer holds JWT at session start; on chunk/stop, ownership re-verified, JWT refreshed if near expiry.
- If JWT expires mid-session: chunk/stop returns 401; extension refreshes token, retries idempotent.
- Staging writes use current valid JWT; archive commit uses fresh JWT from user click.

---

## Task Instructions (Checkbox)

- [x] Document occurrence state machine with transitions and triggers
- [x] Define consent UI flow and commit/discard API contracts
- [x] Specify private object layout and authenticated access; no public/signed source URL
- [x] Enumerate lifecycle/commit/discard/delete races with resolution rules
- [x] Define archive manifest schema and database integrity verification
- [x] Specify history/list/read/export/delete contracts and owner-bound pagination
- [x] Replace unsafe owner restore endpoint with operator-only consistency-set/PITR restore
- [x] Write cron job spec for 24h staging expiry and durable Storage API cleanup
- [x] Write backfill script for 0006 (existing runs → one occurrence per historical run)
- [x] Add historical banner to superseded transcript-storage plan/spec
- [x] Document rollback: archive delete never resurrects staging; restore only from an approved backup set

---

## Acceptance & Failure/Race Checks

- Occurrence state transitions enforce valid paths only (no `pending_documents` → `archived` direct)
- Commit idempotent: 10 concurrent commits → 1 archive row, same manifest
- Discard after commit ignored (status `archived` rejects discard)
- 24h expiry cron purges only `awaiting_decision` occurrences
- Staging objects inaccessible after discard (RLS + bucket deletion)
- Archive export paginates every table, verifies manifest hashes/counts, and never returns a successful truncated ZIP
- Failed artifacts commit as explicit `partial_failed`, not an infinite save blocker
- Backup restore works only through the documented operator drill within provider backup retention
- Migration 0006 backfill maps every existing run to an occurrence; no run is orphaned
- Historical 30-day purge is disabled for occurrence-bound runs and does not affect archives

---

## Migration & Cutover

- 0006 creates occurrences; existing runs get `occurrence_id` FK
- 0011 replaces 0005 pg_cron job (staging TTL only; transcript_segments keep 30-day purge)
- Code: audio/transcribe writers check occurrence status before insert
- Popup: new consent UI after meeting end; old `save_transcript` checkbox removed
- No data loss: old transcript segments retained; new model adds occurrence layer

---

## Non-Goals

- No auto-save on expiry
- No team-shared archives
- No raw audio in staging or archive
- No RAG/vector index in this task
- No summary/action items (H-08/H-09 deferred)