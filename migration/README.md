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

## Transkrip final dan retensi (F6)
> **HISTORICAL:** `0004`/`0005` menjelaskan baseline retensi 30 hari. `0011` menonaktifkan job lama; arsip produk saat ini tidak memiliki expiry otomatis.

Setelah `0003`, terapkan [0004_transcript_segments.sql](0004_transcript_segments.sql), aktifkan Supabase **Integrations → Cron** bila belum tersedia, lalu terapkan [0005_transcript_retention_cron.sql](0005_transcript_retention_cron.sql) sebagai administrator database. Migration tidak dijalankan otomatis saat backend startup. Jangan mengaktifkan F6 pada deployment sebelum **keduanya** berhasil dan job aktif terverifikasi.

`0004` menambah pilihan `save_transcript`, status arsip, dan expiry tetap pada `meeting_runs`, serta tabel `transcript_segments`. Run lama tetap opt-out; tidak ada backfill teks dari cache. Data hanya final, owner/run/meeting wajib konsisten, dan unique `(run_id, seq)` membuat append identik idempoten tanpa mengubah final lama. RLS memakai expiry database aktual; transaksi yang menunggu lock memeriksa ulang `clock_timestamp()` setelah lock didapat.

RPC aplikasi `append_transcript_final`, `delete_transcript`, dan `set_transcript_state` tetap `SECURITY INVOKER`. Grants Data API langsung juga dibatasi: consent/expiry immutable, final tidak dapat diubah, DELETE langsung ditolak, dan transisi state `deleted` menjalankan purge fisik yang sama. Penghapusan arsip tidak menghapus run/meeting/counter atau mengizinkan write berikutnya pada run itu. Hanya administrator dapat menjalankan `purge_expired_transcripts()`.

`0005` membutuhkan pg_cron yang benar-benar terpasang/preloaded dan aktif; extension tidak tersedia atau scheduler dijeda menghasilkan error, bukan fallback. Job `livetranslate-transcript-retention` berjalan `* * * * *` dengan command `SELECT public.purge_expired_transcripts();`. Re-apply memperbarui/mengaktifkan satu job bernama sama dan menangani pergantian administrator pemasang. Aplikasi tidak mendapat akses administrator atau service-role key.

Periksa melalui SQL Editor:

```sql
SELECT jobid, jobname, schedule, active, command
FROM cron.job
WHERE jobname = 'livetranslate-transcript-retention';

SELECT d.status, d.start_time, d.end_time, d.return_message
FROM cron.job_run_details d
JOIN cron.job j USING (jobid)
WHERE j.jobname = 'livetranslate-transcript-retention'
ORDER BY d.runid DESC
LIMIT 10;
```

Harus ada satu job aktif dengan jadwal tiap menit dan riwayat eksekusi sukses. Teks tidak dapat diakses tepat saat expiry meski scheduler sedang bermasalah; penghapusan fisik menunggu scheduler pulih. Pantau riwayat operasional tanpa memasukkan isi transkrip ke query/log. Backup/PITR dan log provider mengikuti kebijakan layanan masing-masing, bukan jaminan penghapusan F6.

Harness `sh migration/tests/check-meetings.sh` sekarang menguji `0004` dua kali, RLS, final immutable, direct mutation, purge, FK cascade, dan balapan append/delete/expiry dengan session PostgreSQL terkontrol. Harness biasa tetap memakai PostgreSQL tanpa pg_cron; validasi scheduler harus dijalankan pada runtime yang mendukung extension tersebut. Tidak membaca `.env` atau memodifikasi project cloud.

Saat re-apply migration lama, pertahankan urutan `0001` → `0002` → `0003` → `0004` → `0005`; migration lama dapat mengatur ulang grants yang kemudian diperluas F6. Rollback aplikasi tidak memerlukan DROP tabel atau penghapusan data: pertahankan schema dan job retensi agar arsip yang sudah dibuat tidak kehilangan proteksi retensi. Penghapusan schema/data bukan bagian dari rollback otomatis.

Hasil lokal dan batas aktivasi project target dicatat di [status F6](../docs/implementation/transcript-storage-status.md).

## Occurrence, terjemahan, dokumen, assistance, dan arsip permanen

Setelah `0005`, terapkan migration berikut secara berurutan: [0006 occurrence](0006_meeting_occurrences.sql), [0007 translations](0007_translations.sql), [0008 documents](0008_documents.sql), [0009 questions/answers](0009_questions_answers.sql), [0010 archives](0010_occurrence_archives.sql), dan [0011 staging cleanup](0011_staging_cleanup.sql). `0010` mereferensikan semua tabel artefak sehingga tidak boleh diterapkan sebelum `0007`–`0009`.

`0006` membuat satu occurrence untuk setiap kejadian meeting dan mengikat run/segmen kepada owner+occurrence. `0007` menautkan setiap terjemahan Indonesia ke final sumber. `0008` membuat metadata dokumen, locator chunk, indeks lexical, private Storage bucket, dan batas 10 file/20 MiB/100 MiB. `0009` menyimpan pertanyaan stabil, jawaban bilingual, citation, serta status pemrosesan assistance untuk setiap segmen final. Database hanya menerima citation yang benar-benar cocok dengan chunk dokumen milik owner+occurrence.

`0010` membuat commit arsip idempoten. Manifest schema v2 mencatat count dan SHA-256 transkrip, terjemahan, dokumen, chunk, pertanyaan, jawaban, dan status assistance. Commit ditolak selama derived work belum terminal; dokumen atau provider yang sudah terminal gagal menghasilkan `partial_failed`, bukan save blocker permanen. Fungsi verifikasi menghitung ulang manifest sebelum export. Setelah commit, arsip tidak memiliki expiry.

Discard, expiry, dan explicit archive delete tidak menulis `storage.objects` lewat SQL. Transaksi mengantrikan path di `storage_cleanup_jobs`, menghapus metadata artefak, dan mempertahankan tombstone+manifest archive setelah delete. Backend mencoba penghapusan melalui Storage API dengan JWT owner; antrean gagal diproses worker admin terisolasi sesuai `docs/implementation/operator-runbook.md`.

`0011` mengganti job retensi lama dengan `livetranslate-staging-retention`. Hanya occurrence `awaiting_decision` yang melewati `staging_expires_at` yang dibersihkan; arsip permanen tidak dipurge berdasarkan umur. Job database dijalankan setiap 15 menit dan hanya membentuk worklist object cleanup. Verifikasi job:

```sql
SELECT jobid, jobname, schedule, active, command
FROM cron.job
WHERE jobname IN ('livetranslate-transcript-retention', 'livetranslate-staging-retention');
```

Harus ada satu job staging aktif dan job lama tidak aktif. Jalankan `sh migration/tests/check-meetings.sh` untuk memverifikasi lifecycle, owner isolation, translation linkage, document limits/locator, citation quote membership, completeness barrier, partial archive, idempotent commit, manifest tamper detection, discard/delete tombstone, durable cleanup worklist, expiry staging, dan race transaksi pada PostgreSQL lokal. Validasi Storage policy, Storage API worker, dan Cron tetap wajib pada Supabase target karena harness PostgreSQL lokal tidak menyediakan schema `storage` atau `pg_cron`.

