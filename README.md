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
