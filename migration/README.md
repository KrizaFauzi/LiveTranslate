# Migrasi Supabase

Folder ini berada di root proyek. `0000_profiles.sql` adalah migrasi custom SQL untuk Drizzle migrator; `meta/_journal.json` menentukan urutan migrasi. SQL ditulis manual karena dependency Drizzle Kit tidak dapat diunduh di lingkungan authoring. Tidak ada klaim bahwa migrasi sudah dijalankan.

Migrasi membuat `public.profiles`, FK ke `auth.users`, RLS, grants kolom, trigger profil/timestamp, backfill, dan role `livetranslate_runtime` NOLOGIN. Struktur internal Supabase Auth tidak dibuat ulang. Drizzle menyimpan riwayat apply pada `drizzle.__drizzle_migrations`.

## Apply

1. Gunakan project Supabase uji terlebih dahulu dan review SQL.
2. Isi `MIGRATION_DATABASE_URL` dengan koneksi migration berprivilege yang memakai SSL. Gunakan direct connection atau session pooler; migration dapat membutuhkan hak CREATE ROLE/GRANT.
3. Dari root:

```bash
cd auth-service
npm install
node --env-file=../.env node_modules/drizzle-kit/bin.cjs migrate
```

Tidak ada migrasi otomatis saat startup. Jangan menjalankan `push` pada database remote. Apply kedua melalui migrator tidak mengulang migrasi yang tercatat; menjalankan SQL mentah dua kali bukan alur yang didukung.

## Role runtime

Migrasi membuat role NOLOGIN bila belum ada; aktifkan LOGIN dan atur password kuat melalui administrasi database yang aman. Jangan menyimpan password dalam SQL/Git atau menyalinnya ke log. Role harus NOINHERIT, NOSUPERUSER, NOBYPASSRLS, NOCREATEROLE dan bukan owner tabel. Jika role sudah ada, periksa dan koreksi keanggotaan/privilege lama sebelum menggunakannya.

`DATABASE_URL` menggunakan user `livetranslate_runtime` (atau `livetranslate_runtime.<project-ref>` untuk pooler Supabase). Database runtime membutuhkan akses schema auth untuk helper `auth.uid()` yang lazimnya sudah diberikan kepada role authenticated oleh Supabase. Gunakan SSL; service memaksa verifikasi sertifikat dan menonaktifkan prepared statements agar kompatibel dengan transaction pooler.

Service memasang role authenticated dan klaim hasil verifikasi token pada transaksi query profil. Jangan menggunakan postgres/service_role sebagai koneksi runtime. `MIGRATION_DATABASE_URL` hanya dibaca tool migrasi, bukan container runtime.

## Migrasi berikutnya

Karena baseline awal berupa custom SQL tanpa snapshot code-first, gunakan custom generation untuk menjaga urutan journal:

```bash
cd auth-service
npm run db:generate -- --name=nama_perubahan
```

Isi SQL hasil generate, sesuaikan `src/db/schema.ts`, review lalu apply menggunakan perintah sebelumnya. Jangan menjalankan generate code-first biasa pada baseline ini: tanpa snapshot schema, tabel awal dapat dihasilkan ulang. Jika beralih ke code-first, bentuk dan review baseline snapshot terlebih dahulu pada database uji.

## Uji integrasi opt-in

Test memerlukan **project Supabase disposable yang belum memiliki tabel profiles** dan koneksi admin serta runtime. Role runtime dapat diprovision lebih dahulu; migrasi memberi keanggotaan authenticated. Jangan gunakan project produksi. Test membuat schema dan role; schema/riwayat migrasi dibiarkan untuk inspeksi, sedangkan akun fixture dihapus.

Set `TEST_ALLOW_MIGRATION=yes`, `TEST_DATABASE_URL`, dan `TEST_RUNTIME_DATABASE_URL` pada environment aman, lalu:

```bash
cd auth-service
npm run test:database
```

Tanpa ketiga variabel, test dilewati. Test mencakup apply/no-op, akun lama, trigger nama/default, RLS dua akun/anon, pembatasan kolom dan rollback signup ketika trigger gagal.
