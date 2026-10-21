# LiveTranslate

Chrome extension React dengan backend Go. Login/register memakai Supabase Auth; profile diambil dari PostgreSQL Supabase melalui Data API (PostgREST). Tidak ada service Node atau Drizzle pada backend.

```text
Extension → Go /api/auth/* → Supabase Auth
                         → Supabase Data API → public.profiles (RLS)
```

## Struktur backend

Backend dikelompokkan berdasarkan fitur di `backend/internal/`:

- `auth/`: `handler.go` untuk HTTP dan route, `service.go` untuk alur/validasi autentikasi, `repository.go` untuk akses Supabase Auth/Data API, dan `model.go` untuk struktur data.
- `health/handler.go`: route liveness `/health`.
- `deepgram/check.go` dan `sumopod/check.go`: pemeriksaan provider melalui CLI.
- `config/config.go`: konfigurasi environment.

`cmd/server/main.go` menyusun server dan mendaftarkan route milik setiap fitur. Paket `httpapi` dan `services` tidak digunakan lagi. URL endpoint dan kontrak respons tetap sama.

## Setup

1. Jika `.env` belum ada, salin `.env.example`. Pertahankan nilai yang sudah diisi.
2. Isi tiga konfigurasi Supabase berikut.
3. Terapkan [migration/0001_supabase_data_api.sql](migration/0001_supabase_data_api.sql) melalui SQL Editor Supabase; lihat [panduan migrasi](migration/README.md).
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
(cd backend && go test ./... && go vet ./...)
(cd extension && npm test && npm run build)
node --test --test-isolation=none http/auth.test.mjs
docker compose config --quiet
```

Hasil pengujian dan batas verifikasi ada di [status implementasi](docs/implementation/auth-status.md).

## Tab Meeting Capture

Terapkan [migration/0002_meeting_sessions.sql](migration/0002_meeting_sessions.sql) setelah `0001`, build ulang backend/extension, lalu Reload extension melalui `chrome://extensions` dan setujui akses situs meeting. Service worker mendeteksi tab aktif pada jendela Chrome yang difokuskan tanpa perlu membuka popup. Popup menampilkan status sinkronisasi dan ID sesi.

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

Terapkan [migration/0003_meeting_runs.sql](migration/0003_meeting_runs.sql) setelah `0002`, build ulang backend/extension, lalu Reload extension melalui `chrome://extensions`. Extension mendapat izin tambahan `tabCapture` dan `offscreen`; tidak ada akses mikrofon.

Capture hanya berjalan setelah pengguna menekan "Mulai merekam" di popup dan hanya untuk tab meeting yang statusnya `registered`. Worker memilih meeting-nya sendiri, sehingga popup tidak dapat mengarahkan capture ke meeting lain. Audio diambil dari tab meeting dengan `chrome.tabCapture.getMediaStreamId` dan dokumen offscreen `USER_MEDIA`, lalu diambil sebagai PCM16 mono dari `AudioWorklet` (bukan `MediaRecorder`) dan diunggah berurutan sebagai frame biner 100 ms. Sample rate dikirim saat membuat run dan diteruskan ke Deepgram sebagai `encoding=linear16`.

Container webm/opus tidak dipakai karena hasil `MediaRecorder` tidak pernah menghasilkan segmen final di Deepgram; PCM16 mentah menghasilkan segmen partial dan final.

Audio tidak disimpan di mana pun: backend mengukur byte dan chunk, lalu membuangnya. Yang tersimpan hanya metadata run (`meeting_runs`) berisi status, jumlah byte, jumlah chunk, dan waktu mulai/berhenti. Batas ukuran 2 MiB per chunk dan 1 GiB per run.

Capture berhenti saat pengguna menekan "Berhenti merekam", tab meeting ditutup, pengguna logout, atau terjadi error. Menutup popup tidak menghentikan capture.

Kontrak API (seluruh endpoint membutuhkan `Authorization: Bearer <access_token>`):

| Endpoint | Kontrak |
| --- | --- |
| `POST /api/audio/runs` | Body `{"meeting_id":"<uuid>","sample_rate":48000}`; respons `{"run":{...}}`; 404 bila meeting bukan milik pengguna; 400 bila `sample_rate` tidak positif; 503 `deepgram_config` bila konfigurasi provider kosong; 502 `deepgram_unreachable` bila sesi provider gagal dibuka |
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
- Popup menampilkan daftar final lalu partial terakhir. Popup yang dibuka ulang menerima backlog segmen yang terlewat lewat query `since`.
- Kalau transkripsi gagal, capture dihentikan dan popup menampilkan "Transkripsi gagal. Capture dihentikan".
- **Audio maupun transkrip tidak pernah disimpan** di database, log, atau file. Yang tersimpan hanya metadata run (`meeting_runs`): status, jumlah byte, jumlah chunk, dan waktu mulai/berhenti. Backlog segmen hanya hidup di memori backend.

Status dan bukti verifikasi ada di [status audio capture](docs/implementation/audio-status.md); desain lengkap ada di [spec transkripsi](docs/superpowers/specs/2026-10-21-audio-transcription-design.md).
