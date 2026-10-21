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

## Meeting berbasis link

Setelah `0001`, jalankan [0002_meeting_sessions.sql](0002_meeting_sessions.sql) melalui SQL Editor project yang sama. Migration ini tidak dijalankan otomatis oleh server. Tabel `meeting_sessions` menyimpan satu sesi permanen per pengguna dan identitas ruang. `meeting_key` serta `canonical_url` merupakan generated columns; query/passcode tidak disimpan. Unique constraint `(user_id, meeting_key)` mencegah duplikasi antar tab dan request paralel.

RLS mengizinkan pengguna membaca dan membuat sesi miliknya. Grants INSERT hanya mencakup identitas provider dan pemilik; UPDATE/DELETE tidak diberikan. Backend memakai JWT pengguna dan publishable key. Tidak ada tabel history LLM pada fitur ini; `meeting_sessions.id` menjadi kontrak pengenal downstream.

Tes lokal migrasi/RLS dapat dijalankan dengan `sh migration/tests/check-meetings.sh` dari root. Docker memakai image `postgres:18-alpine`, tanpa jaringan dan tanpa port host, serta menghapus container setelah tes. Tes tidak membaca `.env` atau mengakses Supabase. Image harus tersedia lokal atau dapat diunduh Docker terlebih dahulu. Harness SQL di `migration/tests/` hanya untuk container kosong, bukan SQL Editor Supabase.

## Run capture audio

Setelah `0002`, jalankan [0003_meeting_runs.sql](0003_meeting_runs.sql) melalui SQL Editor project yang sama. Tabel `meeting_runs` menyimpan metadata satu sesi capture: id, user_id, meeting_id, status, bytes_received, chunks_received, started_at, dan ended_at. Audio tidak pernah disimpan; migration ini hanya mencatat how much data mengalir.

`meeting_key` tidak ada di tabel ini: run selalu terikat `meeting_id` milik pengguna yang sama. RLS mengizinkan SELECT, INSERT, dan UPDATE untuk baris sendiri. Fungsi `append_audio_chunk` dan `stop_audio_run` dipakai backend untuk penjumlahan byte/chunk yang atomik dan penutupan run yang idempoten; keduanya `SECURITY INVOKER`, jadi hak akses dan RLS role pemanggil tetap berlaku, dan `anon` tidak diberi EXECUTE.

Harness lokal di `migration/tests/meeting_runs.sql` menguji terpasangnya migration dua kali, akuntansi byte dan chunk, idempotensi stop, penolakan chunk setelah run ditutup, isolasi akun lain, dan penolakan akses `anon`. Jalankan bersama harness sesi meeting lewat `sh migration/tests/check-meetings.sh`.
