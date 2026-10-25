# Autentikasi LiveTranslate — Supabase HTTP API

Implementasi saat ini: React extension → backend Go → Supabase Auth/Data API. Pengguna menyetujui penggantian service Node/Drizzle dan pekerjaan langsung di main. Dokumen ini menggantikan status arsitektur Node/Drizzle sebelumnya; spec/plan auth lama tetap menjadi riwayat.

## Perubahan

- `backend/internal/auth/` (`handler.go`, `service.go`, `repository.go`, `model.go`): register/login/refresh/logout langsung ke Supabase Auth REST. GET `/api/auth/me` memvalidasi token melalui `/auth/v1/user`, lalu mengambil `public.profiles` melalui `/rest/v1/profiles` dengan JWT pengguna dan filter ID yang telah diverifikasi.
- `backend/internal/config/config.go`: membaca URL Supabase, publishable/legacy anon key dan redirect. URL Supabase diparse tanpa pembatasan tambahan scheme/host/path; kesalahan koneksi ditangani saat request. Konfigurasi wajib dan penolakan secret/service_role key tetap berlaku. Redirect HTTP hanya diizinkan untuk localhost/loopback. File konfigurasi terpisah `supabase.go` dihapus.
- Pemeriksaan CLI Supabase, `backend/internal/services/supabase.go`, dan test-nya dihapus. Akses Supabase diperiksa melalui alur autentikasi dan profile; opsi `-check` hanya menerima `deepgram` atau `sumopod`.
- `compose.yaml`: hanya service backend; service Node dan dependencies Drizzle dihapus. Node tetap diperlukan untuk tooling/build frontend.
- `.env` dan `.env.example`: hapus AUTH_SERVICE_URL, AUTH_INTERNAL_TOKEN, DATABASE_URL, MIGRATION_DATABASE_URL. Nilai Supabase/AI pada file lokal dipertahankan. Cadangan privat `.env.before-supabase-api` diabaikan Git dan memiliki permission 0600, sama dengan `.env`.
- `migration/0001_supabase_data_api.sql`: setup idempotent untuk project baru atau skema lama; tabel, grants, RLS, trigger signup/timestamp, backfill akun lama dan reload schema cache Data API. Tidak menghapus akun, data profile, atau role lama. Migrasi 0000 dan journal lama dipertahankan sebagai riwayat.
- Endpoint dan bentuk response frontend/Resterm tetap sama. Tidak ada perubahan pada extension yang dibutuhkan.

## Struktur fitur

Route dan penulisan respons berada di auth/handler.go, alur autentikasi di auth/service.go, akses Supabase di auth/repository.go, dan struktur data di auth/model.go. Health memiliki handler sendiri. Pemeriksaan provider berada dalam paket deepgram dan sumopod. Entry point hanya menyusun server dan mendaftarkan route. Test kontrak auth dipindahkan bersama fitur; test registrasi route menyeluruh berada di cmd/server/main_test.go.

## Alur HTTP

| Endpoint Go | Supabase |
| --- | --- |
| POST /api/auth/register | POST /auth/v1/signup; display_name pada data metadata; redirect_to dari konfigurasi server |
| POST /api/auth/login | POST /auth/v1/token?grant_type=password |
| POST /api/auth/refresh | POST /auth/v1/token?grant_type=refresh_token |
| POST /api/auth/logout | POST /auth/v1/logout?scope=local dengan JWT pengguna |
| GET /api/auth/me | GET /auth/v1/user, lalu GET /rest/v1/profiles dengan JWT yang sama |

Signup dengan sesi menghasilkan 201; signup yang memerlukan konfirmasi menghasilkan 202. Login/refresh mengubah response token Supabase menjadi `{user, session}` yang sudah dipakai frontend. `/me` menghasilkan `{user, profile}`. Tidak ada password yang disimpan di tabel profiles.

## Database

`public.profiles`: id UUID PK/FK auth.users ON DELETE CASCADE, display_name varchar(100), created_at dan updated_at timestamptz. Ketiga kolom selain id wajib; timestamps default now(). Trigger membuat profile dari metadata pendaftaran dan memperbarui updated_at. RLS membatasi akses ke auth.uid() = id; authenticated hanya boleh SELECT dan UPDATE display_name; anon tidak mendapat akses tabel.

## Error yang dapat ditelusuri

| Code | Arti/tindakan |
| --- | --- |
| supabase_config (503) | Pesan menyebut konfigurasi kurang/tidak valid; perbaiki env dan recreate backend |
| auth_unreachable / auth_timeout (502/504) | Koneksi backend ke Supabase Auth gagal/timeout |
| auth_api_error (502) | Supabase Auth memberi status tidak sukses; pesan menyebut HTTP upstream |
| unauthorized (401) | Kredensial, konfirmasi email, atau sesi tidak valid |
| profile_unreachable / profile_timeout (502/504) | Koneksi ke Data API gagal/timeout |
| profile_api_error (502) | Periksa Data API aktif, schema public diekspos, tabel, grants dan RLS; pesan menyebut HTTP upstream |
| profile_missing (503) | Tidak ada baris terlihat; periksa migrasi/backfill dan policy SELECT |
| auth_response_invalid / profile_response_invalid (502) | Payload upstream tidak sesuai kontrak |

Detail SQL, token, password, dan body error upstream tidak diteruskan ke client/log. Timeout yang terjadi ketika body response sedang dibaca saat ini dilaporkan sebagai response_invalid 502; timeout sebelum response diterima dilaporkan 504.

## Verifikasi pada sesi ini

- Test Go: LULUS, termasuk register dengan/tanpa sesi, session projection, refresh, logout lokal, query own profile dengan JWT, respons profile kosong/salah pengguna, error provider, input invalid, rate limit dan timeout.
- Go vet dan build binary: LULUS.
- Test frontend: 7/7 LULUS; build Vite 7.3.7 LULUS.
- Script regression Resterm: 7/7 LULUS.
- Compose config: LULUS.
- Pemeriksaan env: semua variabel lama sudah dihapus dari file aktif; tiga key konfigurasi Supabase tersedia. Nilai rahasia tidak dicetak. File rahasia/cadangan diabaikan Git.
- Review kode independen: tidak ada temuan Critical/Important. Validasi config URL/key/redirect belum memiliki unit test tersendiri; perilaku konfigurasi valid dipakai oleh test handler dan konfigurasi kosong diuji.
- Build/recreate container: LULUS pada 2026-10-21; daemon Docker dapat diakses, image dibangun ulang, dan container `livetranslate-init-backend-1` berjalan dengan `GET /health` → `{"service":"livetranslate-backend","status":"ok"}`.
- HTTP live Resterm: LULUS pada 2026-10-21 lewat `curl` langsung ke backend container; lihat bagian "Validasi live" di bawah.
- Pemeriksaan CLI Supabase sudah dihapus. Akses Supabase live terverifikasi melalui workflow autentikasi/profile pada project nyata.
- Apply SQL, akses RLS nyata: LULUS pada 2026-10-21 untuk `0001` dan `0002`. Email confirmation dan Chrome: BELUM DIUJI. Tidak ada request AI berbayar dijalankan.

## Validasi live — 2026-10-21

Project Supabase nyata (host `qtstmnzovmrxatflonum.supabase.co`, PostgreSQL 17.11) dipakai langsung. Tidak ada request AI berbayar dan tidak ada akun yang dihapus.

| Pemeriksaan | Hasil | Bukti |
| --- | --- | --- |
| Migrasi 0001 terpasang | LULUS | `public.profiles` ada dengan kolom `id uuid`, `display_name varchar`, `created_at`, `updated_at`; trigger `livetranslate_auth_user_created` ada pada `auth.users` |
| Trigger profil | LULUS | Insert `auth.users` membuat baris `public.profiles` otomatis dengan `display_name` dari metadata |
| Migrasi 0002 terpasang | LULUS | Diterapkan ke project nyata; `public.meeting_sessions` ada, policy `meeting_sessions_select_own` (SELECT) dan `meeting_sessions_insert_own` (INSERT) untuk role `authenticated`; `anon` tidak mendapat grant |
| Data API melihat tabel baru | LULUS | Setelah `NOTIFY pgrst, 'reload schema'`, query anon berubah dari 404 PGRST205 menjadi 42501 permission denied (tabel ada, akses ditolak) |
| Login | LULUS | `POST /api/auth/login` → 200 dengan `{user, session}`, `expires_at` terisi, `token_type` bearer |
| Profile pengguna | LULUS | `GET /api/auth/me` → 200 `{user, profile}`; `profile` berisi `id`, `display_name`, `created_at`, `updated_at` dari tabel `public.profiles` |
| Refresh | LULUS | `POST /api/auth/refresh` → 200 dengan access token baru (rotasi terbukti) dan `/me` tetap 200 setelah rotasi |
| Logout | LULUS | `POST /api/auth/logout` → 204 tanpa body |
| Token tidak valid | LULUS | `GET /api/auth/me` dengan token palsu → 401 `unauthorized` |
| Password salah | LULUS | `POST /api/auth/login` → 401 `unauthorized`, tanpa membocorkan detail upstream |
| Endpoint meeting tanpa token | LULUS | `GET /api/meetings` tanpa Authorization → 401 `unauthorized` |
| Pemetaan error upstream | LULUS | Email tak valid → upstream 400 diterjemahkan `registration_failed` (400); kuota email habis → upstream 429 diterjemahkan `rate_limit` (429). Body upstream tidak diteruskan ke client |
| `Cache-Control` | LULUS | Semua response auth memakai `Cache-Control: no-store` |

Batas validasi live:

- Jalur register yang sukses (signup → sesi) belum dapat dibuktikan: project membatasi pengiriman email konfirmasi dan upheld memberi 429 `over_email_send_rate_limit`. Yang terbukti: pemetaan error register, dan trigger profil `0001` bekerja saat baris `auth.users` ada.
- Email konfirmasi nyata belum diuji karena domain uji tidak dapat menerima email.
- Akun uji dibuat di luar GoTrue sebagai tindakan operator. GoTrue mensyaratkan kolom string `auth.users` bernilai string kosong, bukan `NULL`; `NULL` membuat password grant gagal dengan 500 `Database error querying schema`. Perbaikan: set `confirmation_token`, `email_change`, `email_change_token_current`, `email_change_token_new`, `recovery_token`, `reauthentication_token`, `phone_change`, `phone_change_token` menjadi `''`.
- Dua akun uji dibuat dengan email `@livetranslate.invalid` (domain tidak dapat delivering) dan password `LtE2e-2026!test`: `livetranslate.e2e@…` dan `livetranslate.e2e2@…`.
- Chrome tetap belum diuji: tidak ada binary Chrome di lingkungan ini.

## Tindakan operator

1. Terapkan `migration/0001_supabase_data_api.sql` di SQL Editor project Supabase yang sama. Bisa untuk setup baru atau skema lama; tidak perlu menjalankan 0000 dahulu. **Selesai 2026-10-21.**
2. Aktifkan Data API dan pastikan public ada di Exposed schemas. **Sudah aktif pada project yang diuji.**
3. Periksa tiga konfigurasi Supabase di `.env`; tambahkan `http://localhost:8989/auth/confirmed` di Authentication → URL Configuration → Redirect URLs.
4. Jalankan `docker compose up -d --build --remove-orphans` agar backend memakai kode/env baru dan auth-service lama dihapus. **Selesai 2026-10-21.**
5. Registrasi/konfirmasi email jika diperlukan, lalu jalankan `resterm run -e local -E http/resterm.env.json -W auth-session -f text http/auth.http` dengan akun uji. Workflow memeriksa Health, Login, Profile, Refresh, Profile, Logout. **Login sampai Logout sudah terbukti live lewat HTTP langsung; alur signup tertahan rate limit email project.**
6. Uji register happy path setelah kuota email project pulih: `POST /api/auth/register` dengan email uji, lalu konfirmasi akun lewat dashboard atau `UPDATE auth.users SET confirmed_at = now() WHERE email = ...`.

Dokumentasi Supabase diambil melalui Context7 dari dokumentasi resmi Supabase Auth dan Supabase. Tool Obsidian tidak tersedia pada sesi ini; salinan dokumen diantrekan melalui docs/obsidian-pending/pending.md.
