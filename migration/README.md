# Migrasi Supabase

Aplikasi sekarang memakai Supabase Auth dan Data API lewat HTTPS. Tidak membutuhkan Drizzle, DATABASE_URL, atau role PostgreSQL khusus untuk runtime.

## Terapkan melalui SQL Editor

1. Buka project yang sama dengan SUPABASE_URL pada dashboard Supabase.
2. Buka SQL Editor → New query.
3. Salin seluruh [0001_supabase_data_api.sql](0001_supabase_data_api.sql), lalu Run menggunakan role administrasi bawaan SQL Editor.
4. Pastikan Data API aktif dan schema `public` masuk Exposed schemas di pengaturan API project. Cari pengaturan Data API melalui dashboard jika susunan menu berbeda.
5. Periksa `public.profiles` melalui Table Editor. Akun yang sudah ada akan mendapat profile melalui backfill.

`0001` dapat diterapkan pada project baru maupun project yang sudah memakai `0000`; dapat dijalankan ulang tanpa menimpa nama profile. Transaksi membatalkan perubahan jika ada kegagalan. Migrasi tidak berjalan otomatis saat server startup. Status apply SQL Editor tidak dicatat ke journal Drizzle.

## Bentuk database

| Kolom public.profiles | Tipe | Aturan |
| --- | --- | --- |
| id | uuid | Primary key; FK auth.users.id; ON DELETE CASCADE |
| display_name | varchar(100) | Wajib; nama setelah trim sepanjang 1–100 karakter |
| created_at | timestamptz | Wajib; default now() |
| updated_at | timestamptz | Wajib; diperbarui trigger |

Supabase Auth tetap mengelola akun, password, dan sesi di schema `auth`. Trigger membuat profile ketika akun dibuat. RLS membatasi SELECT/UPDATE ke `auth.uid() = id`; hanya kolom display_name dapat diubah oleh authenticated. Anon tidak mendapat akses tabel.

Backend mengambil profile dengan publishable/legacy anon key dan JWT pengguna, sehingga policy RLS tetap berlaku. Jangan memakai secret/service_role key.

`0000_profiles.sql` dan `meta/_journal.json` dipertahankan sebagai riwayat migrasi lama. Jangan jalankan `0000` untuk setup baru; gunakan `0001` saja. Role livetranslate_runtime yang pernah dibuat dibiarkan, tetapi tidak lagi dipakai aplikasi. Tidak ada tabel atau data akun yang dihapus oleh perpindahan ini.

Untuk perubahan database berikutnya, simpan SQL baru bernomor urut di folder ini, review, lalu terapkan secara eksplisit melalui SQL Editor.
