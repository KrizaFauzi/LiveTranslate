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
- Build/recreate container: BELUM DIUJI; akses daemon Docker ditolak pada docker.sock.
- HTTP live Resterm: TERHALANG, Health gagal dengan `socket: operation not permitted`, exit 21. Tidak ada klaim workflow end-to-end lulus.
- Pemeriksaan CLI Supabase sudah dihapus. Akses Supabase live tetap belum terverifikasi melalui workflow autentikasi/profile.
- Apply SQL, akses RLS nyata, email confirmation dan Chrome: BELUM DIUJI. Tidak ada perubahan database remote atau request AI berbayar dijalankan.

## Tindakan operator

1. Terapkan `migration/0001_supabase_data_api.sql` di SQL Editor project Supabase yang sama. Bisa untuk setup baru atau skema lama; tidak perlu menjalankan 0000 dahulu.
2. Aktifkan Data API dan pastikan public ada di Exposed schemas.
3. Periksa tiga konfigurasi Supabase di `.env`; tambahkan `http://localhost:8989/auth/confirmed` di Authentication → URL Configuration → Redirect URLs.
4. Jalankan `docker compose up -d --build --remove-orphans` agar backend memakai kode/env baru dan auth-service lama dihapus.
5. Registrasi/konfirmasi email jika diperlukan, lalu jalankan `resterm run -e local -E http/resterm.env.json -W auth-session -f text http/auth.http` dengan akun uji. Workflow memeriksa Health, Login, Profile, Refresh, Profile, Logout.

Dokumentasi Supabase diambil melalui Context7 dari dokumentasi resmi Supabase Auth dan Supabase. Tool Obsidian tidak tersedia pada sesi ini; salinan dokumen diantrekan melalui docs/obsidian-pending/pending.md.
