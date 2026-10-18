# Usulan autentikasi dan database LiveTranslate — 2026-10-18

Status: USULAN, belum disetujui dan belum diimplementasikan.
Sumber lokal: docs/research/2026-10-18-auth-database-proposal.md

## Tujuan dan konteks
Login/register email-password pada popup Chrome React 19.2.0/Vite 7.1.7. Backend saat ini Go net/http; koneksi pemeriksaan database memakai pgx. Instruksi terbaru mewajibkan Drizzle untuk koneksi database dan folder /migration di root proyek. Repository lama live_translate/ dipertahankan.

Drizzle merupakan ORM JavaScript/TypeScript. Ia tidak dapat dipasang sebagai driver Go. Rekomendasi: pertahankan Go sebagai API publik dan integrasi AI, tambah service Node.js/TypeScript internal untuk autentikasi dan akses profil memakai Drizzle + postgres.js. Service internal tidak mempublikasikan port ke host. Alternatif: pindahkan backend ke Node.js, tetapi itu memperluas perubahan termasuk integrasi Deepgram. Drizzle hanya sebagai generator migrasi dengan koneksi runtime pgx tidak memenuhi permintaan koneksi database melalui Drizzle.

## Bentuk database
Supabase Auth mengelola auth.users (identitas, email, hash password, status konfirmasi), auth.sessions dan auth.refresh_tokens. Tidak membuat ulang atau memigrasikan struktur internal tersebut.

Satu tabel aplikasi baru: public.profiles.
- id: uuid, primary key, foreign key ke auth.users(id), ON DELETE CASCADE; tidak membuat UUID terpisah.
- display_name: varchar(100), NOT NULL; setelah trim harus berisi 1–100 karakter.
- created_at: timestamptz, NOT NULL, DEFAULT now().
- updated_at: timestamptz, NOT NULL, DEFAULT now(); trigger memperbarui pada UPDATE.

Relasi auth.users 1:1 public.profiles. Email tidak diduplikasi. Primary key id sudah menjadi indeks yang diperlukan untuk lookup profil.

Trigger AFTER INSERT pada auth.users membuat profil dalam transaksi signup yang sama. Nama dari metadata divalidasi; jika tidak tersedia gunakan nilai default aman. Fungsi trigger memakai SECURITY DEFINER, search_path kosong dan nama tabel berkualifikasi. Migrasi melakukan backfill untuk akun yang sudah ada.

Aktifkan RLS. Anon tidak mendapat akses profil. Role authenticated hanya SELECT/UPDATE baris dengan auth.uid() = id; UPDATE memakai USING dan WITH CHECK. Tidak ada kebijakan INSERT/DELETE langsung bagi pengguna. Batasi update kolom ke display_name; updated_at dikelola trigger. Koneksi backend memakai role runtime terpisah dengan hak minimum, query selalu menggunakan UUID dari token terverifikasi; jika memasang konteks RLS untuk Drizzle lakukan SET LOCAL role/claims dalam transaksi yang sama. Jangan berasumsi koneksi owner/postgres otomatis dilindungi RLS. Kredensial migrasi berprivilege dipisahkan dari runtime.

## Alur komunikasi
FE extension -> Go /api/auth/* -> service auth internal.
- Register: validasi email/password/display_name -> Supabase Auth signup -> trigger profil. Jika konfirmasi email aktif, tampilkan instruksi verifikasi dan jangan menganggap pengguna sudah login.
- Login: Supabase Auth memverifikasi kredensial -> token/sesi diteruskan ke extension.
- GET /api/auth/me: verifikasi access token dengan Supabase Auth, lalu query profil melalui Drizzle dengan identitas hasil verifikasi.
- Refresh/logout: gunakan lifecycle sesi Supabase Auth. Token akses yang sudah terbit bisa tetap berlaku sampai kedaluwarsa setelah logout.
- Verifikasi email pada extension: pengguna mengikuti link email lalu kembali login; detail redirect/callback perlu dipastikan pada implementasi.
- Usulan penyimpanan token: chrome.storage.session dengan akses trusted extension contexts, agar bertahan saat popup ditutup. Penutupan/restart browser meminta login ulang untuk scope awal.
- FE hanya menghubungi API Go. DATABASE_URL, kredensial migrasi dan secret backend tidak masuk bundle extension. Produksi memakai HTTPS.
- Endpoint autentikasi diberi batas ukuran body, timeout, rate limiting, dan pesan error yang tidak membocorkan keberadaan akun.

## Migrasi yang direncanakan
/migration/ diartikan sebagai folder migration/ pada root repository.
- Definisi tabel Drizzle pada auth-service/src/db/schema.ts.
- Konfigurasi drizzle.config.ts menunjuk output migration/ di root.
- SQL tabel, foreign key, indeks PK, grants, RLS, trigger signup/update, dan backfill disimpan bersama metadata migrasi Drizzle.
- Gunakan custom SQL migration untuk trigger/grants/backfill sesuai kebutuhan. Review SQL agar tidak membuat ulang schema auth atau tabel internal Supabase.
- Generate dan apply dipisahkan; tidak menjalankan migrasi otomatis pada startup atau menjalankan schema push ke database remote.
- Untuk mode transaction pooler postgres.js memakai prepare: false.
- Pemeriksaan koneksi database Go yang sekarang memakai pgx perlu diarahkan ke service Drizzle bila persyaratan seluruh akses database melalui Drizzle diterapkan.

## Validasi yang diperlukan setelah persetujuan
Register, login salah/benar, email belum terverifikasi, refresh/logout, akses tanpa token, akses profil pengguna lain, rollback transaksi signup saat trigger gagal, dan migrasi pada database uji termasuk backfill. Build extension serta test/vet backend Go. Belum ada pengujian fitur atau koneksi database pada tahap usulan.

## Referensi
- https://orm.drizzle.team/docs/get-started/supabase-new
- https://orm.drizzle.team/docs/drizzle-config-file
- https://supabase.com/docs/guides/auth/managing-user-data
- https://supabase.com/docs/guides/auth/passwords
- https://supabase.com/docs/guides/database/postgres/row-level-security

