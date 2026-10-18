# LiveTranslate

Chrome extension React dengan backend Go. Login/register memakai Supabase Auth; profil disimpan di PostgreSQL Supabase melalui Drizzle pada auth-service Node.js.

```text
Extension -> Go API -> auth-service -> Supabase Auth / Drizzle PostgreSQL
```

## Setup

1. Siapkan .env dari .env.example jika belum ada. Pertahankan file yang sudah diisi.
2. Isi konfigurasi Supabase dan token internal pada .env.
3. Terapkan migrasi mengikuti [migration/README.md](migration/README.md).
4. Jalankan server:

```bash
docker compose up -d --build
```

Backend tersedia di http://localhost:8989. Auth-service hanya diakses oleh backend Go.

Konfigurasi auth yang diperlukan:

| Variabel | Isi |
| --- | --- |
| AUTH_INTERNAL_TOKEN | Token acak yang sama untuk kedua service; sudah dibuat pada workspace ini |
| SUPABASE_URL | Project URL dari dialog Connect di Supabase |
| SUPABASE_PUBLISHABLE_KEY | Publishable key dari Settings > API Keys |
| SUPABASE_AUTH_REDIRECT_URL | http://localhost:8989/auth/confirmed untuk lokal |
| DATABASE_URL | Koneksi SSL memakai role livetranslate_runtime |
| MIGRATION_DATABASE_URL | Koneksi berprivilege khusus tool migrasi |

Tambahkan redirect yang sama di Supabase > Authentication > URL Configuration > Redirect URLs. Deployment remote memakai HTTPS. Kredensial hanya disimpan di server.

## Extension

```bash
cd extension
npm ci
npm run build
```

Buka chrome://extensions, aktifkan Developer mode, lalu Load unpacked folder extension/dist. Daftar atau masuk menggunakan email/password biasa. Sesi bertahan saat popup ditutup dan berakhir saat browser ditutup.

## HTTP dengan Resterm

Isi email/password pada http/resterm.env.json, lalu buka:

```bash
resterm -e local -E http/resterm.env.json http/auth.http
```

Pilih Health, Register, Login, GetProfile, Refresh, atau Logout. Konfirmasi email setelah registrasi jika diperlukan. [Panduan HTTP](http/README.md).

## Pemeriksaan layanan

```bash
docker compose run --rm backend -check supabase
docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav
docker compose run --rm backend -check sumopod
```

Deepgram dan Sumopod memerlukan key/model pada .env. Gunakan [fixture audio](fixtures/README.md) untuk Deepgram. Pemeriksaan tersebut hanya berjalan saat dipanggil.

Untuk log dan menghentikan server:

```bash
docker compose logs -f backend auth-service
docker compose down
```

## Verifikasi pengembangan

```bash
(cd auth-service && npm test)
(cd extension && npm test && npm run build)
(cd backend && go test ./... && go vet ./...)
docker compose config --quiet
```

Instal dependency auth-service dengan npm install sebelum build/typecheck. Detail auth, migrasi, dan batas pengujian tersimpan di [status implementasi](docs/implementation/auth-status.md).
