# 03 — Realtime, Extension & Translation

> **CURRENT — Execution instructions approved on 2026-10-24.** Reuse the existing capture/transcription baseline and implement the revised occurrence, offer, translation, and UI lifecycle.

---

## Goal

Define Chrome extension behavior: meeting detection, automatic connection offer (not capture), native capture validation, existing PCM16/SSE reuse, translation pipeline, language support research gate, transcript source linkage, document upload UI, archive confirmation, accessibility, and account change handling — without implementation code.

---

## Dependencies

- `01-architecture-data.md` (API contracts, occurrence lifecycle)
- `02-lifecycle-archive.md` (staging, consent)
- Master plan `../2026-10-24-execution-master.md`
- Approved PRD: `../../specs/2026-10-24-product-realignment-design.md` §5, §7, PRD-01..PRD-05, PRD-14

---

## Existing Subsystems

- `extension/src/` — Vite React MV3: `App.jsx` (popup), `background-runtime.js` (SW), `audio-capture.js`, `offscreen.js`, `config.js`
- `backend/internal/audio/`, `transcribe/` — run/chunk/stop, Deepgram streaming, SSE broker
- `backend/internal/meeting/` — resolve, list, detail
- `migration/0002_meeting_sessions.sql`, `0003_meeting_runs.sql`
- `fixtures/meeting-urls.json` — 30+ test URLs

---

## Proposed Contracts & Behavior

### 1. Meeting Detection & Connection Offer

| Aspect | Specification |
|---|---|
| Detection | Content script on supported hosts (Google Meet, Zoom Web App, Jitsi meet.jit.si). URL canonicalization per C-05/C-06/C-07. No DOM joined-state (DEC-02 deferred). |
| Trigger | Tab activation, URL change, page load complete, window focus. No popup open required. |
| Offer | Background SW sends `connectionOffer` to popup (if open) or stores in `chrome.storage.session`. Offer includes `meeting_session_id`, canonical URL, provider. |
| User Action | User clicks "Terhubung" → POST `/api/occurrences/connect` `{meeting_session_id}` → creates occurrence `pending_documents`. Returns `occurrence_id`. |
| Reject/Dismiss | User closes offer or clicks "Nanti" → no occurrence created. Same URL re-visit re-offers (dedup by session). |
| Capture Start | Only after connection + user clicks "Mulai merekam" in popup. Worker verifies occurrence `pending_documents` or `active`. |

### 2. Native Capture Validation (P0-2 Gate)

| Check | Method |
|---|---|
| `chrome.tabCapture.getMediaStreamId` | Requires `tabCapture` permission; user must approve per session |
| Offscreen document `USER_MEDIA` | Created by SW; `AudioWorklet` produces PCM16 mono 100ms frames |
| Audio format | `AudioContext` sample rate sent at run create; Deepgram `encoding=linear16` |
| Permissions | Manifest: `tabCapture`, `offscreen` (`USER_MEDIA`), `storage`, `alarms`. Host permissions: meeting providers + backend. No `audioCapture`, no global `tabs`. |
| Verification | Manual test in Chrome unpacked: offer appears → connect → start → frames sent → chunk 200 → stop → SSE segments. No binary Chrome in CI; document as manual gate. |

### 3. Reuse Existing Audio Pipeline

- PCM16 frames from `AudioWorklet` → binary chunks → `POST /api/audio/runs/{id}/chunks`
- Writer bounded queue (128 finals / 2 MiB), 10s request deadline
- Deepgram streaming via backend: handshake deadline, write timeout, teardown budget
- SSE `/api/audio/runs/{id}/transcript` → `segment` (partial/final) + `end` + `error`
- **No auto-reconnect** Deepgram (out of scope). Connection drop → transcription terminal error → capture stops, run status `failed`.

### 4. Translation Pipeline

| Stage | Specification |
|---|---|
| Trigger | Final transcript segment received by backend (after STT `final` status) |
| Input | Segment text, source language (from Deepgram), segment ID, occurrence ID |
| Output | Translation segment: `language: 'id'`, `text`, `source_segment_id`, `source_revision`, `status` (success/failed/partial) |
| Latency target | p95 ≤ 3s after final STT segment received (Desain §11) |
| Failure | Translation failure does not stop capture/transcription. Source segment retained. Status `failed` surfaced to UI. |
| Revision | If STT revises final (rare), translation re-generated with new `source_revision`. UI shows latest. |
| Provider | Sumopod chat completions (non-stream for P0-3). Model from `SUMOPOD_MODEL` env. Key only in backend. |
| Language matrix | **Research gate:** Test supported source languages on real Deepgram/Sumopod account before final model selection. Document unsupported languages explicitly. |

### 5. Transcript–Translation Linkage

- Translation stores `source_segment_id` FK to `transcript_segments`
- UI shows paired rows: source text (original language) ↔ translation (Indonesia)
- Partial segments: translation not generated until `final`
- Source revision tracked: if transcript final changes, translation regenerates, revision increments

### 6. Popup UI Components

| Component | Behavior |
|---|---|
| Connection Offer | Banner at top: "Meeting terdeteksi: [provider] [room]. Terhubung ke LiveTranslate?" Buttons: Terhubung / Nanti |
| Live Translation Panel | Streaming list: final segments paired with translation. Partial shown as "..." with source. Source reference clickable. |
| Question Panel | Numbered list from assistance (04). Each: question text, answer ID+EN, citation. `-` for no evidence. |
| Document Upload | Drag-drop or file picker. Validates type/size. Shows extraction status. Links to occurrence. |
| Archive Confirmation | After meeting end: "Simpan hasil meeting?" with preview counts. Buttons: Simpan / Tidak. |
| History | List archived occurrences with date, duration, preview. Click → detail view (02). |
| Account | Shows email, display_name. Logout clears session. Account switch clears staging. |

### 7. Accessibility (I-11)

- Focus order: offer → connect → start capture → live panel → question panel → doc upload → archive
- `aria-live="polite"` on streaming transcript/translation
- `aria-live="assertive"` on error/status changes
- Labels on all buttons/inputs; keyboard operable
- Color contrast WCAG AA

### 8. Account Change & Limits

- Logout / account switch: clear `chrome.storage.session`, `chrome.storage.local` meeting mappings, pending outbox. Occurrence staging on server unaffected.
- New account: fresh connection offer on meeting detection.
- One active capture per account (enforced by backend: second start returns 409).
- Occurrence cap 4h (server-enforced): after 4h, occurrence → `awaiting_decision`, capture stops gracefully.

### 9. Backpressure & Limits

- Audio chunk queue: 50 frames max (5s), newer frames dropped when full
- SSE subscriber buffer: 100 segments, older dropped
- Translation queue: bounded per occurrence, global cap
- Rate limit: auth endpoints per B-09; meeting endpoints per S-09 (to design)

---

## Task Instructions (Checkbox)

- [ ] Document meeting detection triggers and connection offer flow
- [ ] Specify native capture validation checklist (manual gate)
- [ ] Document existing PCM16/SSE pipeline reuse (no changes to audio-capture/offscreen)
- [ ] Define translation pipeline: trigger, input, output, failure handling, revision
- [ ] Specify language support research gate (test matrix on real provider accounts)
- [ ] Document transcript–translation linkage and source revision tracking
- [ ] Specify popup UI components and states
- [ ] Document accessibility requirements
- [ ] Define account change handling and one-capture limit
- [ ] Add historical banner to `docs/superpowers/plans/2026-10-21-audio-transcription.md` and `specs/2026-10-21-audio-transcription-design.md`
- [ ] List manual verification steps for Chrome unpacked native capture

---

## Acceptance & Failure/Race Checks

- Connection offer appears on supported meeting URL without popup open
- Offer dismissed → no occurrence created; re-visit re-offers
- Capture starts only after connect + explicit "Mulai merekam"
- PCM16 frames sent, Deepgram SSE segments received (3 partial + 1 final minimum)
- Translation generated for each final segment, linked to source
- Translation failure does not stop capture; error surfaced in UI
- Language matrix tested on real provider account before model commit
- Popup accessible: focus order, aria-live, labels, contrast
- Account switch clears local state; server staging persists
- 4h cap enforced: capture stops, occurrence → `awaiting_decision`

---

## Migration & Cutover

- Legacy `meeting_sessions` → `meeting_occurrences` via 0006 backfill
- Popup: remove pre-capture `save_transcript` checkbox; add post-meeting archive confirmation
- Audio pipeline unchanged (reuse existing PCM16/SSE)
- Translation new: adds `translations` table, backend service, popup panel

---

## Non-Goals

- No joined-state DOM detection (DEC-02 deferred)
- No auto-reconnect Deepgram
- No speaker diarization (DEC-06 deferred)
- No mic capture (DEC-04 / D-10 DI LUAR BATAS)
- No subtitle sync latency measurement (E-09 PERLU VALIDASI)
- No multi-target translation selector (F-03: Indonesia only)