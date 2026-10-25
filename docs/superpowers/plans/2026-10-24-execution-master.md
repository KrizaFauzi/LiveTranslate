# Eksekusi Planning — Master Dokumentasi

> **CURRENT — Implementation authorized after planning approval.** This document remains the execution contract; implementation status and evidence are tracked in `../../implementation/feature-roadmap.md` and `.superpowers/sdd/2026-10-24-execution-master/progress.md`.

**Desain acuan:** `../specs/2026-10-24-product-realignment-design.md` (DISETUJUI)
**Roadmap kanonis:** `../../implementation/feature-roadmap.md`
**Status baseline:** Lihat `../../implementation/transcript-storage-status.md`, `auth-status.md`, `meeting-status.md`, `audio-status.md`
**Batas sesi planning historis:** Larangan implementasi pada sesi penyusunan telah digantikan oleh instruksi eksplisit pengguna untuk menjalankan paket ini.

---

## 1. Tujuan Paket

Menyusun satu set dokumen planning yang **bisa dieksekusi AI lain** tanpa perlu bertanya balik desain. Setiap detail berisi: tujuan, dependensi, lokasi subsistem existing, kontrak perilaku, checkbox instruksi, acceptance, race/failure checks, migrasi/cutover, dan non-goals. Tanpa kode implementasi.

---

## 2. Urutan Eksekusi (P0-0 sampai P0-7)

Sesuai desain §12, urutan wajib:

| Urutan | Paket | Status eksekusi | Kunci validasi tersisa |
|---|---|---|---|
| P0-0 | Konsolidasi PRD/plan lama | SELESAI LOKAL | Tidak ada; roadmap kanonis |
| P0-1 | Identitas occurrence, owner-only, consent pascameeting, arsip tanpa expiry | SELESAI LOKAL | Migration Supabase target |
| P0-2 | Capture tab-only + tawaran koneksi + lifecycle | PERLU VALIDASI | Chrome unpacked dan meeting nyata |
| P0-3 | Terjemahan Indonesia + catatan sumber tertaut | PERLU VALIDASI | Sumopod nyata, matriks bahasa, p95 |
| P0-4 | Upload/ekstraksi/retrieval/citation | PERLU VALIDASI | Storage target, PDF scan dan corpus nyata |
| P0-5 | Pertanyaan terstruktur + jawaban bilingual bounded | PERLU VALIDASI | Model/corpus multilingual nyata |
| P0-6 | Arsip lengkap, history, hapus/ekspor, pemulihan | PERLU VALIDASI | Storage export dan restore drill |
| P0-7 | Security/failure/latency gates + distribusi privat | PERLU VALIDASI | TLS/secrets/cloud, soak, outage drills |

**Dependency wajib:** P0-1 sebelum P0-2/P0-3/P0-4/P0-5/P0-6. P0-4 sebelum P0-5. P0-6 bergantung P0-1 dan P0-5. Detail paralel setelah kontrak bersama disepakati.

---

## 3. Struktur File Planning

```
docs/superpowers/plans/
  2026-10-24-execution-master.md          # Dokumen ini
  2026-10-24-details/
    01-architecture-data.md               # Arsitektur modular, auth, schema, API, event
    02-lifecycle-archive.md               # Occurrence/run, consent, staging, arsip, cutover
    03-realtime-extension.md              # Chrome detection, tawaran, capture, transkripsi, terjemahan, UI
    04-documents-assistance.md            # Parser, retrieval, citation, question mapping, bilingual
    05-security-operations-validation.md  # Threat model, performance budgets, release, backup
```

Setiap detail memuat:
- `## Goal` — satu kalimat
- `## Dependencies` — paket/detail sebelumnya yang harus selesai
- `## Existing Subsystems` — path existing yang terverifikasi via `find` (jangan duplikat dump source)
- `## Proposed Modules/Contracts` — antarmuka perilaku yang AI pelaksana harus pertahankan
- `## Task Instructions` — checkbox per langkah planning (bukan kode)
- `## Acceptance & Failure/Race Checks` — bukti verifikasi yang dibutuhkan
- `## Migration & Cutover` — rencana data/versi lama ke baru
- `## Non-Goals` — eksplisit di luar cakupan

---

## 4. Pemetaan Fitur Roadmap ke Paket

| Roadmap ID | Paket Planning | Status implementasi ringkas |
|---|---|---|
| A-01..A-12 | 01-architecture-data | Arsitektur modular-monolith terpasang; item non-P0 tetap mengikuti roadmap |
| B-01..B-15 | 01-architecture-data, 02-lifecycle-archive | Auth tersedia; validasi target dan fitur akun non-P0 tetap terbuka |
| C-01..C-25 | 02-lifecycle-archive, 03-realtime-extension | Occurrence lifecycle terpasang; joined-state DEC-02 dan native Chrome terbuka |
| D-01..D-12 | 03-realtime-extension | Capture tab-only terpasang; reconnect di luar P0 |
| E-01..E-10 | 03-realtime-extension | Transkripsi terpasang; browser/provider nyata tetap gate |
| F-01..F-08 | 03-realtime-extension | Translation worker/API/UI terpasang; provider/language/latency perlu validasi |
| G-01..G-12 | 02-lifecycle-archive | Consent pascameeting, staging, archive permanen, export/delete terpasang |
| H-01..H-13 | 04-documents-assistance | Dokumen, retrieval, pertanyaan, bilingual evidence-only answer terpasang |
| I-01..I-14 | 03-realtime-extension | Surface P0 terpasang; accessibility/native UI tetap gate |
| J-01..J-07 | 01-architecture-data, 05-security-operations-validation | Deepgram/Sumopod adapter terpasang; paid-provider validation terbuka |
| S-01..S-12 | 05-security-operations-validation | Owner RLS, prompt/citation guard, bounds terpasang; cloud threat validation terbuka |
| L-01..L-06 | 05-security-operations-validation | Health/readiness, trace/log, runbook dan restore procedure terpasang |
| M-01..M-15 | 05-security-operations-validation | Local suites/build/smoke lulus; live/soak/outage matrix terbuka |
| N-01..N-06 | 05-security-operations-validation | Production image/docs/rollback tersedia; private deployment belum dirilis |

---

## 5. Penandaan Planning Lama (Historis)

Plan/spec berikut **tidak dihapus**, tetapi diberi banner di heading utama:

| File | Ganti dengan |
|---|---|
| `docs/superpowers/plans/2026-10-18-agent-initialization.md` | Master execution plan ini |
| `docs/superpowers/plans/2026-10-18-auth-implementation.md` | 01-architecture-data auth section |
| `docs/superpowers/specs/2026-10-18-auth-design.md` | 01-architecture-data auth section |
| `docs/superpowers/plans/2026-10-21-audio-transcription.md` | 03-realtime-extension |
| `docs/superpowers/specs/2026-10-21-audio-transcription-design.md` | 03-realtime-extension |
| `docs/superpowers/plans/2026-10-23-transcript-storage.md` | 02-lifecycle-archive |
| `docs/superpowers/specs/2026-10-23-transcript-storage-design.md` | 02-lifecycle-archive |

Banner format (disisipkan di heading 1):
```markdown
> **HISTORICAL — Superseded by 2026-10-24 execution planning.** This document describes an earlier architecture (Node/Drizzle auth, pgx DB, pre-consent storage). See `../plans/2026-10-24-execution-master.md` and detail files for current design. Contents preserved for history; do not execute.
```

---

## 6. Vocabulary Kontrak Bersama

| Istilah | Definisi |
|---|---|
| `room` | Existing `meeting_id` — identitas link/ruang meeting stabil |
| `occurrence` | Satu pertemuan aktual milik satu user (owner+room+started_at), independen dari capture run |
| `run` | Satu percobaan capture audio (ada `meeting_run_id`), terikat occurrence |
| `staging` | Data sementara sebelum keputusan simpan: transcript, terjemahan, dokumen, extraction, jawaban |
| `archive` | Paket disetujui permanen: manifest, catatan, Q/A bilingual, dokumen sumber, kutipan |
| `owner-only` | Semua akses data (tabel, file, retrieval, ekspor) dibatasi `user_id` pemilik via RLS |

---

## 7. Parameter Desain yang Disetujui

| Parameter | Nilai | Sumber |
|---|---|---|
| Backend | Satu Go modular, MV3 React/JS | Desain §4 |
| Auth/Team | Private invite, owner-only | Desain §10, PRD-14 |
| Consent arsip | Setelah meeting, bukan sebelum capture | PRD-11 |
| Arsip isi | Catatan, Q/A bilingual, dokumen, kutipan | PRD-12 |
| Arsip retensi | Tanpa expiry otomatis | PRD-13 |
| Staging window | 24 jam setelah end meeting | Desain §6 usulan |
| Occurrence cap | 4 jam per occurrence | Desain §6 usulan |
| Dokumen | PDF text/OCR, DOCX, XLSX, CSV; 10 file, 20 MiB/file, 100 MiB total | Desain §8 |
| Jawaban | Indonesia + Inggris baku; `-` tanpa bukti | PRD-09, PRD-10 |
| Pertanyaan | Semua peserta lain, tanpa filter penerima | PRD-05 |
| Revisi AI | Maks 1 putaran, deadline 15 detik | Desain §9, §11 |
| Concurrency target | 3 user × 1 meeting bersamaan | Desain §11 |

---

## 8. Instruksi Per Detail (Ringkas)

### 01-architecture-data.md
- Modul Go, routing, config, provider adapters
- Supabase Auth/Data API/RLS kontrak
- Auth: register/login/refresh/logout/me + private team provisioning (no public signup)
- Schema: `profiles`, `meeting_sessions` (room), `meeting_occurrences` (baru), `meeting_runs`, `transcript_segments`, `translations` (baru), `documents` (baru), `document_chunks` (baru), `questions` (baru), `answers` (baru), `archives` (baru)
- API surface: semua endpoint §7 roadmap + `/api/occurrences`, `/api/documents`, `/api/questions`, `/api/archives`
- Migration cutover dari pgx/auth-Node ke HTTP API, 0001/0002既存, 0003 runs, 0004/0005 transcript lama → new migrations 0006 occurrences, 0007 translations, 0008 documents/chunks, 0009 questions/answers, 0010 archives, 0011 purge policy (staging TTL only)
- Historical banner linking

### 02-lifecycle-archive.md
- Occurrence lifecycle: `pending_documents` → `active` → `awaiting_decision` (24h) → `archived` | `discarded`
- Run terikat occurrence; multi-run per occurrence allowed
- Consent simpan setelah `awaiting_decision`; staging TTL 24h; archive permanen
- Staging storage: private bucket/folder, signed URL singkat, RLS owner-only
- Save/discard idempoten; race staging vs save vs crash vs credential expiry
- Archive manifest: JSON listing all objects + integrity hashes
- History/list/read/delete/export endpoints
- Rollback: delete archive tidak resurrect staging; restore archive via manifest only
- Backup/restore plan private objects + DB
- Historical banner linking

### 03-realtime-extension.md
- Chrome content script: URL detection only (no joined-state DOM), canonicalization, supported hosts
- Background SW: tab activation, URL change, focus, auto-offer connection (not capture)
- Popup: connection offer, live translation panel, transcript source-linked, question panel, doc upload, archive confirmation
- Capture: existing PCM16/AudioWorklet/offscreen reuse; start requires user click; stop on tab close/navigate/logout/transcription terminal error; no mic
- Transcription: existing Deepgram SSE partial/final; transcript source IDs linked to translation
- Translation: segment→Indonesia; revision tracking; source reference; failure status not empty transcript
- Language matrix research gate: test supported languages on real provider account before final model selection
- Reconnect/gaps: backlog bounded, no auto-reconnect Deepgram (out of scope P0-2)
- Accessibility: focus order, aria-live, loading/error states
- Account change: clears session staging, re-offers on new meeting
- Historical banner linking

### 04-documents-assistance.md
- Parsers per format: PDF text, PDF OCR, DOCX, XLSX, CSV; shared output: `DocumentVersion`, `SectionLocator`, `ExtractedText`, `QualityFlags`
- Extraction limits: size, pages, cells, decompression, timeout; reject encrypted/password; no macro/formula execution; cached formula values only if stored
- Citation stability: document version + locator; re-upload = new version; old citations point to old version
- Retrieval: owner+occurrence isolation; lexical/hybrid evaluation on representative multilingual corpus before choosing embedding; no speculative vector DB
- Question pipeline: final segments → detect questions → assign stable question_id + source segment IDs → retrieval → draft bilingual → citation/grammar check → max 1 revision if needed → finalize; `-` if no evidence
- Deduplication: same question text + same source segments within occurrence = same question_id; follow-up changing meaning = new id
- Prompt injection defense: document content treated as untrusted data; no system instruction override; retrieval authorization deterministic
- Provider selection gate: test Sumopod model quality/latency/cost on real account before committing
- Failure modes distinct: parse fail, retrieval fail, provider timeout, quota exceeded, no evidence — each surfaced not collapsed to `-`

### 05-security-operations-validation.md
- Threat model matrix: token, URL, audio, transcript, prompt, RLS, private objects — owner-only application ≠ provider/operator guarantees
- Bounded queues: per-meeting AI jobs, global cap, p95 translation ≤3s, p95 Q/A ≤8s at 3 concurrent
- Cost safety: token budgets, quota alerts, degrade gracefully (no auto-pay at startup/test)
- Resilience: rate limit handling, provider outage, SSE reconnect, crash recovery
- Observability: structured logs (no secrets), health/readiness split, cost metrics, trace IDs
- Operator docs: deploy, migrate, rotate keys, restore, capacity monitoring
- Release: clean build, manifest valid, no secrets, rollback plan, private distribution
- Reproducibility: existing test commands (go test -race, go vet, npm test, build, contract auth, migration harness) — no new test infra required

---

## 9. Verifikasi Planning

Checklist sebelum serahkan paket:
- [ ] Semua 15 PRD-ID tertutupi oleh task di detail
- [ ] Semua keputusan DEC-01..DEC-18 tertutupi atau ditandai deferred
- [ ] Roadmap katalog status tidak diubah (hanya dokumen planning)
- [ ] Plan lama semua diberi banner historis
- [ ] Migration paths dari baseline existing ke target desain eksplisit
- [ ] Non-goals eksplisit per detail
- [ ] Tidak ada kode/pseudocode/fungsi body di dokumen planning
- [ ] Referensi lintas detail via `../2026-10-24-execution-master.md` dan `../../specs/2026-10-24-product-realignment-design.md`

---

## 10. Serahan

Paket detail telah disusun dan implementasi diotorisasi. Pelaksana wajib memakai urutan P0-0…P0-7, mencatat ruling/bukti di ledger, dan tidak menaikkan status provider/browser nyata tanpa validasi yang benar-benar dijalankan.