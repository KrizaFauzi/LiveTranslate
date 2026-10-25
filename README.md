# LiveTranslate

LiveTranslate adalah Chrome extension React dengan backend Go untuk transkripsi audio tab meeting, terjemahan langsung ke bahasa Indonesia, bantuan jawaban berbasis dokumen, dan arsip meeting permanen. Login/register memakai Supabase Auth; seluruh data aplikasi diakses melalui Supabase Data API dengan RLS owner-only. Tidak ada service Node, Drizzle, Redis, atau penyimpanan audio mentah.

```text
Chrome extension (audio tab, tanpa mikrofon)
  → backend Go
      → Supabase Auth + Data API + private Storage
      → Deepgram streaming transcription
      → Sumopod translation dan evidence-only bilingual assistance
  → staging occurrence 24 jam
      → Simpan: arsip permanen
      → Tidak: metadata dibuang dan object cleanup diantrikan
```

## Status saat ini

Paket P0 telah selesai dan diverifikasi secara lokal. Alur yang tersedia: tawaran koneksi meeting, capture atas persetujuan pengguna, transkrip dan terjemahan live, upload PDF/DOCX/XLSX/CSV per meeting, pemetaan pertanyaan, jawaban Indonesia/Inggris dengan kutipan, keputusan simpan/tidak setelah meeting, serta history/detail/export/delete arsip.

Status belum production-ready. Migration `0006`–`0011`, Storage/Cron Supabase target, Chrome unpacked dengan `tabCapture` nyata, Sumopod nyata, matriks bahasa/latensi, soak test, dan restore drill masih perlu divalidasi. Status kanonis dan bukti terbaru ada di [feature roadmap](docs/implementation/feature-roadmap.md).

## Struktur backend

Backend dikelompokkan berdasarkan fitur di `backend/internal/`:

- `auth/`, `meeting/`, `occurrence/`: autentikasi, identitas ruang, dan lifecycle satu kejadian meeting.
- `audio/`, `transcribe/`: capture PCM16, Deepgram SSE, staging transkrip, dan terjemahan.
- `documents/`, `assistance/`: upload/ekstraksi PDF-DOCX-XLSX-CSV, retrieval lexical, pertanyaan, jawaban bilingual, dan kutipan.
- `health/handler.go`: liveness `/health` dan readiness konfigurasi `/ready`.
- `deepgram/` dan `sumopod/`: adapter/pemeriksaan provider. Secret hanya dibaca backend.
- `config/config.go`: konfigurasi environment.

`cmd/server/main.go` menyusun server dan mendaftarkan route milik setiap fitur. Paket `httpapi` dan `services` tidak digunakan lagi. URL endpoint dan kontrak respons tetap sama.

## Setup

1. Jika `.env` belum ada, salin `.env.example`. Pertahankan nilai yang sudah diisi.
2. Isi konfigurasi Supabase dan provider pada `.env`. `SUPABASE_URL` wajib HTTPS; HTTP hanya diterima untuk host loopback saat pengembangan lokal.
3. Terapkan migration `0001` sampai `0011` sesuai urutan di [panduan migrasi](migration/README.md). Nomor `0006`–`0011` menerapkan occurrence, terjemahan, dokumen, assistance, arsip permanen, dan cleanup staging.
4. Aktifkan Data API dengan schema `public` pada pengaturan API project.
5. Tambahkan redirect yang sama pada Supabase → Authentication → URL Configuration → Redirect URLs.
6. Build dan jalankan backend:

```bash
docker compose up -d --build --remove-orphans
```

Backend tersedia di http://localhost:8989. `--remove-orphans` menghapus container auth-service lama dari project Compose ini.

| Variabel .env | Isi |
| --- | --- |
| SUPABASE_URL | Project URL HTTPS, misalnya https://PROJECT.supabase.co |
| SUPABASE_PUBLISHABLE_KEY | Publishable key dari Settings → API Keys; legacy anon key juga didukung |
| SUPABASE_AUTH_REDIRECT_URL | http://localhost:8989/auth/confirmed untuk lokal; HTTPS saat deployment |
| DEEPGRAM_API_KEY / DEEPGRAM_MODEL / DEEPGRAM_LANGUAGE | Kredensial/model transkripsi; bahasa sumber `multi` harus divalidasi pada akun nyata |
| SUMOPOD_API_KEY / SUMOPOD_MODEL | Kredensial/model terjemahan dan jawaban; tidak dipanggil saat startup/test biasa |
| SUMOPOD_BASE_URL | Default ketat `https://ai.sumopod.com/v1` |

`AUTH_SERVICE_URL`, `AUTH_INTERNAL_TOKEN`, `DATABASE_URL`, dan `MIGRATION_DATABASE_URL` tidak digunakan lagi. Saat perpindahan ini, `.env` lama dicadangkan di `.env.before-supabase-api` yang diabaikan Git. Nilai Supabase/AI yang sudah ada dipertahankan. Jangan memakai secret/service_role key.

## Extension

```bash
cd extension
npm ci
npm run build
```

Buka chrome://extensions, aktifkan Developer mode, lalu Load unpacked folder extension/dist. Daftar atau masuk menggunakan email/password. Konfirmasi email jika Supabase memintanya, kemudian login melalui extension. Sesi disimpan di chrome.storage.session.

Kontrak frontend tetap sama: POST `/api/auth/register`, `/login`, `/refresh`, `/logout` dan GET `/api/auth/me`. Respons login/refresh tetap `{user, session}`; `/me` tetap `{user, profile}`. Semua endpoint auth memakai Cache-Control: no-store.

## HTTP dengan Resterm

Isi email/password akun uji pada `http/resterm.env.json` (diabaikan Git), lalu:

```bash
resterm -e local -E http/resterm.env.json http/auth.http
resterm run -e local -E http/resterm.env.json -W auth-session -f text http/auth.http
```

Registrasi dan konfirmasi email dilakukan dahulu jika belum ada akun. Workflow menguji Health → Login → Profile → Refresh → Profile → Logout. [Panduan HTTP](http/README.md).

Error kini menyebut tahap kegagalan: `supabase_config` untuk konfigurasi, `auth_api_error` untuk Auth, `profile_api_error` untuk Data API, dan `profile_missing` jika baris profile tidak ditemukan/terhalang RLS. Restart saja tidak memperbaiki migrasi atau grants yang belum diterapkan.

## Pemeriksaan layanan

```bash
docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav
docker compose run --rm backend -check sumopod
```

Akses Supabase Auth dan profile/RLS diperiksa melalui workflow Resterm dengan akun login. Deepgram dan Sumopod memerlukan key/model pada `.env` dan hanya dipanggil secara eksplisit.

```bash
docker compose logs -f backend
docker compose down
```

## Verifikasi pengembangan

```bash
(cd backend && go test -race -count=1 -timeout=120s ./... && go vet ./...)
(cd extension && npm test && npm run build)
node --test --test-isolation=none http/auth.test.mjs
sh migration/tests/check-meetings.sh
docker compose config --quiet
docker compose build
```

Baseline terakhir: backend race/vet dan kedua binary lulus, extension **151/151** beserta build lulus, auth contract **7/7** lulus, migration/RLS/concurrency harness lulus, production image berhasil dibangun, dan container aktual merespons `/health` serta `/ready`. Validasi ini lokal dan tidak menggantikan gate layanan nyata.

## Tab Meeting Capture

Setelah seluruh migration `0001`–`0011` diterapkan, build ulang backend/extension, Reload extension melalui `chrome://extensions`, dan setujui akses situs meeting. Service worker mendeteksi tab aktif pada jendela Chrome yang difokuskan tanpa perlu membuka popup. Popup menawarkan **Terhubung** atau **Nanti**, lalu menampilkan status sinkronisasi dan ID sesi.

Provider yang didukung:

- Google Meet: `https://meet.google.com/abc-defg-hij` dengan format kode ruang resmi.
- Zoom browser: domain `zoom.us`/subdomain, path `/wc/{meeting-id}/join`, `/wc/{meeting-id}/start`, serta bentuk `/wc/join/{meeting-id}` dan `/wc/start/{meeting-id}`. ID berupa 9–11 digit. Link `/j/...` yang meluncurkan aplikasi desktop tidak didaftarkan.
- Jitsi: `https://meet.jit.si/{room}`; nama ruang ASCII 1–128 huruf/angka/underscore/hyphen. Kapitalisasi nama disatukan mengikuti identitas ruang Jitsi. Domain self-hosted dan path bertingkat belum didukung.

Identitas sesi adalah **pengguna + identitas link kanonis**, bukan ID tab. Refresh, dua tab dengan ruang sama, tutup/buka kembali, dan login ulang sesudah browser restart menggunakan `meeting_id` yang sama. Domain regional Zoom serta variasi path join/start dinormalisasi menjadi satu identitas. Query dan fragment (termasuk passcode/JWT) dibuang sebelum penyimpanan atau pengiriman ke backend. Tidak ada pencocokan fuzzy.

Deteksi hanya mengenali halaman ruang, termasuk prejoin dengan URL sama; tidak memastikan kehadiran atau akhir meeting. Menutup tab tidak menghapus sesi. Ruang yang digunakan lagi di hari berbeda tetap menggunakan sesi/history yang sama. Fitur ini tidak menangkap audio; capture ada di bagian "Audio Capture Meeting" dan tetap memakai `meeting_id` yang sama.

Antrean offline dan cache tanpa token disimpan di `chrome.storage.local`; autentikasi tetap di `chrome.storage.session`. Worker melakukan retry dengan backoff 1–30 menit menggunakan alarm setiap menit. Login ulang akun yang sama melanjutkan antrean; pergantian akun menghapus cache/antrean akun sebelumnya, sedangkan logout eksplisit menghapus state meeting lokal. Data yang sudah tersimpan di backend tetap tersedia saat login kembali.

Kontrak API (seluruh endpoint membutuhkan `Authorization: Bearer <access_token>`):

| Endpoint | Kontrak |
| --- | --- |
| `POST /api/meetings/resolve` | Body `{"canonical_url":"https://meet.google.com/abc-defg-hij"}`; respons `{"meeting":{...}}` dengan ID existing atau sesi baru |
| `GET /api/meetings?limit=50&offset=0` | Respons `{"meetings":[...]}`; limit 1–100 |
| `GET /api/meetings/{id}` | Detail milik pengguna; sesi tidak ada/milik akun lain menghasilkan 404 |

Backend menghitung ulang identitas link dan memverifikasi token melalui Supabase Auth. Semua fitur audio/transkrip/LLM berikutnya harus memeriksa kepemilikan `meeting_id` sebelum membaca atau menulis konteks.

Aturan Chrome: [Tabs API](https://developer.chrome.com/docs/extensions/reference/api/tabs), [worker lifecycle](https://developer.chrome.com/docs/extensions/develop/migrate/to-service-workers). Format browser Zoom: [Zoom Web App](https://support.zoom.com/hc/en/article?id=zm_kb&sysparm_article=KB0064261). Aturan lowercase Jitsi: [getNormalizedRoomName](https://github.com/jitsi/jitsi-meet/blob/master/react/features/base/util/uri.ts).

## Audio Capture Meeting

Setelah seluruh migration diterapkan, build/reload backend dan extension. Extension memakai `tabCapture` dan dokumen `offscreen`; manifest tidak meminta akses mikrofon.

Capture hanya berjalan setelah pengguna menekan "Mulai merekam" di popup dan hanya untuk tab meeting yang statusnya `registered`. Worker memilih meeting-nya sendiri, sehingga popup tidak dapat mengarahkan capture ke meeting lain. Audio diambil dari tab meeting dengan `chrome.tabCapture.getMediaStreamId` dan dokumen offscreen `USER_MEDIA`, lalu diambil sebagai PCM16 mono dari `AudioWorklet` (bukan `MediaRecorder`) dan diunggah berurutan sebagai frame biner 100 ms. Sample rate dikirim saat membuat run dan diteruskan ke Deepgram sebagai `encoding=linear16`.

Container webm/opus tidak dipakai karena hasil `MediaRecorder` tidak pernah menghasilkan segmen final di Deepgram; PCM16 mentah menghasilkan segmen partial dan final.

Audio mentah tidak disimpan: backend meneruskan byte ke Deepgram, menghitung akuntansi run, lalu membuang byte. Final masuk staging privat occurrence secara otomatis; pengguna baru memilih **Simpan** atau **Tidak** setelah meeting selesai. Pilihan simpan mengarsipkan catatan, terjemahan, dokumen, pertanyaan/jawaban, dan kutipan tanpa expiry otomatis. Batas audio 2 MiB per chunk, 1 GiB per run, dan empat jam per occurrence.

Capture berhenti saat pengguna menekan "Berhenti merekam", tab meeting ditutup, transkripsi gagal, atau backend menolak run secara terminal (409 `run_closed`, 502 `deepgram_unreachable`, atau token yang tidak dapat diperbarui). Kesalahan jaringan sementara saat upload tidak menghentikan capture: audio berlubang dan capture berlanjut. Saat stop/logout diminta, extension baru menyelesaikan occurrence atau menghapus sesi setelah drain lokal dan stop backend diakui; kegagalan mempertahankan state untuk retry dan tidak melaporkan keberhasilan palsu. Menutup popup tidak menghentikan capture.

Kontrak API (seluruh endpoint membutuhkan `Authorization: Bearer <access_token>`):

| Endpoint | Kontrak |
| --- | --- |
| `POST /api/audio/runs` | Body `{"meeting_id":"<uuid>","occurrence_id":"<uuid>","sample_rate":48000}` setelah `POST /api/occurrences/{id}/activate`; pilihan arsip sebelum capture ditolak |
| `POST /api/audio/runs/{id}/chunks` | Body biner `application/octet-stream`; respons `{"run":{...}}` dengan akuntansi byte dan chunk; 409 bila run sudah ditutup |
| `POST /api/audio/runs/{id}/stop` | Menghentikan run, memfinalisasi sesi provider, lalu idempoten; respons `{"run":{...}}` |
| `GET /api/audio/runs/{id}/transcript` | Server-Sent Events; query `?since=<seq>`; event `segment`, `end`, `error`; 404 bila run tidak ada atau milik akun lain; 503 `transcript_unavailable` bila run aktif tanpa sesi live |

Status lengkap ada di [status audio capture](docs/implementation/audio-status.md).

## Transkripsi live

Byte audio yang masuk ke backend diteruskan ke Deepgram secara streaming. Extension tidak pernah bicara langsung ke Deepgram; `DEEPGRAM_API_KEY` hanya ada di backend.

Konfigurasi provider:

| Variabel .env | Nilai | Catatan |
| --- | --- | --- |
| DEEPGRAM_API_KEY | secret | Hanya di backend; jangan pernah masuk extension, log, atau repository |
| DEEPGRAM_MODEL | `nova-2` | Model yang diterima akun; `2-general` dan `2-meeting` ditolak akun ini |
| DEEPGRAM_LANGUAGE | `multi` | Bahasa yang diterima akun; `en-US` dan `id` juga diterima, `id-ID` ditolak |

Alur:

```text
offscreen (AudioWorklet -> PCM16 mono 100 ms)
  → POST /api/audio/runs/{id}/chunks  (biner, Bearer)
      → sesi Deepgram streaming (encoding=linear16)
      → akuntansi byte/chunk di meeting_runs
  ← GET /api/audio/runs/{id}/transcript  (SSE: segment, end, error)
      → chrome.storage.session (transcript_state)
          → popup (daftar segmen)
```

- Format wire adalah PCM16 mono mentah dengan sample rate run; segmen `partial` dan `final` keduanya diterima.
- Segmen membawa urutan (`seq`), waktu mulai, durasi, dan bahasa. Partial terbaru menimpa partial sebelumnya; final masuk daftar final dan tidak berubah diam-diam.
- Popup menampilkan daftar final lalu partial terakhir. Popup tidak membuka stream SSE: dokumen offscreen yang membuka stream transkrip sekali saat capture dimulai, lalu menulis setiap segmen ke `chrome.storage.session`. Popup yang ditutup lalu dibuka kembali membaca state itu, jadi transkripnya tetap ada. Query `since` dipakai oleh dokumen offscreen untuk meminta segmen yang belum dimiliki, bukan oleh popup.
- Kalau transkripsi gagal, popup menampilkan "Transkripsi gagal." dan capture dihentikan otomatis (status capture menjadi "Capture dihentikan.").
- Audio dan partial tidak ditulis ke database. Final masuk staging occurrence, diterjemahkan ke Indonesia, dan menjadi sumber deteksi pertanyaan. Staging yang tidak diputuskan dibersihkan setelah 24 jam; arsip yang disetujui tidak dipurge berdasarkan umur.

Status dan bukti verifikasi ada di [status audio capture](docs/implementation/audio-status.md); desain lengkap ada di [spec transkripsi](docs/superpowers/specs/2026-10-21-audio-transcription-design.md).

## Alur occurrence, dokumen, bantuan, dan arsip saat ini

1. Extension menawarkan koneksi pada URL meeting yang didukung. **Terhubung** membuat occurrence; **Nanti** tidak membuat occurrence.
2. Capture dimulai hanya lewat klik pengguna, memakai audio tab tanpa mikrofon. Final ditampilkan bersama terjemahan Indonesia.
3. PDF, DOCX, XLSX, dan CSV diunggah khusus occurrence, maksimum 20 MiB per request/file dan 100 MiB per occurrence. Parser tidak menjalankan macro/formula; XLSX hanya membaca cached value. PDF ditolak eksplisit di atas 200 halaman teks atau 50 halaman OCR; tidak ada truncation diam-diam. PDF scan memakai OCR terisolasi dan berbatas resource.
4. Pertanyaan yang terdeteksi dipetakan berurutan. Satu final dapat memuat sampai 32 pertanyaan terpisah; input di atas batas gagal eksplisit dan tidak diklaim lengkap. Retrieval selalu dibatasi owner+occurrence. Jawaban formal Indonesia dan Inggris hanya dibuat dari chunk dokumen dan menyertakan locator; tanpa bukti atau saat pipeline gagal, kedua jawaban `-`.
5. Setelah stop/tab ditutup/error terminal/batas empat jam, occurrence menunggu keputusan. **Simpan** menunggu setiap final memiliki hasil translation dan status assistance. Kegagalan artefak yang sudah terminal tetap dapat disimpan sebagai `partial_failed`; dokumen gagal yang sumbernya tidak tersedia ditandai eksplisit di export, bukan diklaim lengkap. Commit membuat manifest SHA-256 seluruh jenis artefak. **Tidak** menghapus staging.
6. History memakai keyset pagination owner-only. Detail arsip menampilkan manifest dan metadata dokumen. Export ZIP menghitung ulang hash/count manifest lalu memverifikasi ukuran dan SHA-256 setiap file sumber yang tersedia. Delete mempertahankan tombstone+manifest, menghapus metadata artefak, dan mengantrikan object private untuk Storage API worker; arsip tidak memiliki expiry otomatis.

API tambahan: `/api/occurrences/*`, `/api/documents`, `/api/questions`, `/api/archives`, dan `/api/audio/runs/{id}/translations`. Semua memakai JWT pengguna dan RLS owner-only. Setup cron, worker object cleanup, dan restore operator dijelaskan di [operator runbook](docs/implementation/operator-runbook.md).

## Dokumentasi lanjutan

- [Feature roadmap dan status verifikasi](docs/implementation/feature-roadmap.md)
- [Operator runbook](docs/implementation/operator-runbook.md)
- [Panduan migration](migration/README.md)
- [Master execution plan](docs/superpowers/plans/2026-10-24-execution-master.md)

