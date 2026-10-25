# Tab Meeting Capture — status implementasi

Tanggal: 2026-10-18. Implementasi lokal selesai; migrasi Supabase dan pemeriksaan manual Chrome belum dijalankan. Tidak ada plan implementasi baru, sesuai instruksi pengguna.

## Perilaku

- Service worker MV3 mendeteksi tab aktif pada jendela normal Chrome yang sedang difokuskan, tanpa membuka popup.
- Provider: Google Meet, Zoom web client pada zoom.us/subdomain, Jitsi meet.jit.si dengan ruang ASCII sederhana. Homepage, launcher desktop Zoom, domain tiruan, dan provider lain ditolak.
- Satu link kanonis per pengguna berarti satu sesi permanen. Refresh, dua tab, tutup/buka kembali, dan login ulang sesudah browser restart mendapatkan meeting_id yang sama. Cache miss diselesaikan melalui backend.
- Parameter URL sensitif dan fragment dibuang sebelum penyimpanan maupun request. Room Jitsi dinormalisasi lowercase mengikuti implementasi provider; alias Zoom memakai identitas room yang sama.
- Backend Go menghitung ulang identitas, memverifikasi token melalui Supabase Auth, dan mengakses Data API memakai publishable key + JWT pengguna. Constraint unik dan RLS mempertahankan isolasi pengguna.
- Cache/outbox URL tanpa token disimpan persisten, retry dengan backoff 1–30 menit via alarm setiap menit. Login akun sama memulihkan pending work; akun berbeda membersihkan state sebelumnya. Logout eksplisit membersihkan state lokal setelah token dihapus, termasuk saat ada event tab bersamaan.
- Popup menampilkan status deteksi/sinkronisasi, canonical URL, ID sesi, dan tombol retry.
- Tidak ada audio, DOM joined-state detection, transkripsi, tabel pesan, atau request LLM. meeting_id menjadi kontrak downstream. Link ruang yang dipakai besok tetap sesi sama.

## File utama

- extension/src/meeting-url.js, meetings.js: classifier dan lifecycle cache/outbox.
- extension/src/background-runtime.js, background.js: event tab, kepemilikan auth tunggal pada worker, RPC popup, retry.
- extension/src/extension-client.js dan App.jsx: autentikasi melalui worker serta indikator meeting.
- extension/public/manifest.json dan vite.config.js: permission host terbatas serta build worker MV3.
- backend/internal/meeting/{handler,service,repository,model}.go: resolve/list/detail.
- backend/internal/auth/verify.go: verifier identitas pengguna untuk fitur meeting.
- migration/0002_meeting_sessions.sql: tabel dengan generated identity/link, grants minimum, unique constraint, dan RLS.
- fixtures/meeting-urls.json: fixture bersama frontend/backend.
- migration/tests/check-meetings.sh dan meeting_sessions.sql: pengujian PostgreSQL sementara tanpa jaringan/Supabase.

## Verifikasi

| Pemeriksaan | Status | Bukti/batas |
| --- | --- | --- |
| npm test di extension | LULUS | 41 tes termasuk refresh, tab kedua, restart worker, offline/retry, login ulang, token 401, race navigasi/logout, serta fixture URL |
| npm run build di extension | LULUS | Vite menghasilkan background.js module, popup, dan manifest; keberadaan entry/import hasil build juga diperiksa |
| go test -race -count=1 ./... di backend | LULUS | Auth, route publik, resolve paralel, detail/daftar pemilik, validasi input, dan fixture URL |
| go vet ./... di backend | LULUS | Tidak ada temuan |
| go build -o /tmp/livetranslate-meeting-server ./cmd/server | LULUS | Binary backend berhasil dibangun |
| node --test --test-isolation=none http/auth.test.mjs | LULUS | 7 tes regresi kontrak autentikasi |
| sh migration/tests/check-meetings.sh | LULUS | PostgreSQL 18 sementara: apply dua kali, ID stabil pada conflict, generated identity, foreign insert/read dan anonim ditolak, UPDATE/DELETE ditolak |
| docker compose config --quiet | LULUS | Konfigurasi valid |
| git diff --check | LULUS | Tidak ada error whitespace |
| Review kode | LULUS | Temuan kehilangan outbox saat login, normalisasi Jitsi, dan race logout diperbaiki beserta regresi; review akhir tanpa blocker penting |
| Supabase Data API nyata + migrasi project | LULUS | Migrasi 0002 diterapkan ke project nyata 2026-10-21; `public.meeting_sessions` terlihat PostgREST setelah reload schema; resolve/list/detail, idempotensi, race paralel, dan isolasi dua akun terverifikasi live |
| Chrome unpacked pada meeting nyata | BELUM DIUJI | API browser diuji dengan fake Chrome; belum membuktikan lifecycle end-to-end di Chrome pengguna (tidak ada binary Chrome di lingkungan) |

## Validasi live — 2026-10-21

Dijalankan terhadap project Supabase nyata lewat backend container `livetranslate-init-backend-1` (port host 8989) dengan dua akun uji terpisah. Tidak ada request AI berbayar.

| Pemeriksaan | Hasil | Bukti |
| --- | --- | --- |
| Migrasi 0002 | LULUS | Tabel, generated `meeting_key`/`canonical_url`, unique `(user_id, meeting_key)`, grants SELECT + column-level INSERT untuk `authenticated`, policy SELECT/INSERT owner-only; `anon` tidak mendapat grant |
| Resolve Google Meet | LULUS | `POST /api/meetings/resolve` → 200 dengan `provider=google_meet`, `provider_host=meet.google.com`, `provider_room_id=abc-defg-hij` |
| Idempotensi | LULUS | Resolve kedua untuk link sama mengembalikan `meeting_id` yang sama |
| Kanonikalisasi | LULUS | `?authuser=0#x` dibuang; `canonical_url` kembali `https://meet.google.com/abc-defg-hij` dan `meeting_id` sama |
| Race paralel | LULUS | Tiga resolve paralel menghasilkan satu `meeting_id` yang sama |
| Alias Zoom | LULUS | `https://app.zoom.us/wc/join/12345678901#token` → `provider=zoom`, `provider_host=zoom.us`, canonical `https://zoom.us/wc/12345678901/join` |
| Normalisasi Jitsi | LULUS | `https://meet.jit.si/EngineeringRoom` → `provider_room_id=engineeringroom` |
| Daftar meeting | LULUS | `GET /api/meetings` mengembalikan 3 sesi milik akun uji |
| Detail meeting | LULUS | `GET /api/meetings/{id}` mengembalikan sesi yang diminta |
| Penolakan URL | LULUS | Homepage Meet, domain tiruan Zoom, scheme http, dan path Jitsi bertingkat → 400 `unsupported_meeting` |
| ID asing | LULUS | `GET /api/meetings/{uuid-asing}` → 404 `not_found` |
| Isolasi akun | LULUS | Akun kedua: daftar kosong, akses meeting akun pertama → 404, resolve link sama → `meeting_id` berbeda |
| RLS langsung | LULUS | Query Data API dengan JWT akun kedua ke `meeting_sessions` milik akun pertama → `[]` (blocked oleh policy) |

Batas: Register/signup masih tertahan rate limit email project, dan lifecycle extension di Chrome nyata belum diuji karena tidak ada binary Chrome di lingkungan ini.

## Aktivasi

1. Jalankan migration/0002_meeting_sessions.sql melalui SQL Editor Supabase setelah 0001. **Selesai 2026-10-21 (diterapkan ke project nyata, policies dan grants terverifikasi).**
2. Build/restart backend: docker compose up -d --build backend. **Selesai 2026-10-21.**
3. Build extension: npm run build di extension. Reload extension/dist pada chrome://extensions dan pastikan izin situs tersedia.
4. Login, buka halaman ruang yang didukung, periksa ID sesi; refresh dan buka link sama pada tab kedua untuk memastikan ID tetap.
5. Pastikan akun lain tidak dapat membaca ID sesi tersebut. **Terbukti live pada 2026-10-21 lewat backend dan Data API.** Jangan menguji fixture SQL lokal terhadap Supabase project yang sudah ada.

Implementasi lama live_translate/ tidak disentuh. Tool MCP Obsidian tidak tersedia; dokumen ini masih menunggu sinkronisasi ke Obsidian Vault / Proyek/LiveTranslate/Notes/2026-10-18-meeting-status.md.
