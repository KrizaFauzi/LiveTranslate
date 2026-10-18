# Desain login dan register LiveTranslate

Tanggal: 2026-10-18.
Status: arsitektur dan skema disetujui pengguna; perintah "Jalankan" setelah review mengotorisasi implementasi. Bukti dan batas verifikasi dicatat di docs/implementation/auth-status.md.
Sumber lokal: docs/superpowers/specs/2026-10-18-auth-design.md.
Dasar: docs/research/2026-10-18-auth-database-proposal.md.

## Tujuan dan cakupan
Pengguna extension dapat membuat akun menggunakan nama, email dan password, melakukan login, melihat identitas akun dan logout. Sesi bertahan saat popup ditutup, tetapi tidak setelah browser restart. Email/password ditangani Supabase Auth. Akses tabel aplikasi menggunakan Drizzle.

Tidak mencakup OAuth, reset password, fitur meeting, atau perubahan integrasi AI. Jangan mengubah implementasi lama jika ditemukan. Pemeriksaan saat ini menunjukkan root workspace bukan repository Git dan path live_translate/ tidak tersedia; jangan membuat atau menghapus repository untuk mengatasi keadaan ini.

## Arsitektur yang disetujui
React/Vite extension -> Go net/http API publik -> auth-service Node.js/TypeScript internal -> Supabase Auth atau Drizzle/postgres.js -> Supabase PostgreSQL.

Go mempertahankan /health dan integrasi Deepgram/Sumopod. Service autentikasi menggunakan Node.js LTS yang versinya dipin saat implementasi, SDK Supabase JavaScript, Drizzle dan postgres.js. Gunakan HTTP bawaan Node untuk menghindari framework tambahan. Dependensi dipin dan lockfile disimpan.

Auth-service di Compose mendengarkan :3001 tanpa port host. Go mengakses http://auth-service:3001 melalui AUTH_SERVICE_URL. Setiap request internal, termasuk pemeriksaan database, memakai X-Internal-Token yang diverifikasi service; Go tidak mempercayai header internal dari client. Token wajib dikonfigurasi di kedua service; tidak ada default secret.

Go dan auth-service harus tetap dapat melayani liveness tanpa kredensial provider. Endpoint auth yang belum dikonfigurasi mengembalikan 503. Permintaan memiliki deadline 10 detik; tidak ada retry otomatis untuk register/login/logout.

## Database
Supabase mengelola auth.users, auth.sessions dan auth.refresh_tokens. Migrasi aplikasi tidak membuat ulang atau mengubah definisi tabel internal tersebut.

Tabel baru public.profiles:
| Kolom | Tipe | Ketentuan |
| --- | --- | --- |
| id | uuid | PK, FK auth.users(id), ON DELETE CASCADE, tanpa UUID default |
| display_name | varchar(100) | NOT NULL, panjang btrim 1–100 karakter |
| created_at | timestamptz | NOT NULL, DEFAULT now() |
| updated_at | timestamptz | NOT NULL, DEFAULT now(), trigger sebelum UPDATE |

Email hanya disimpan oleh Supabase Auth. Indeks primary key cukup untuk query profil menurut id.

Trigger AFTER INSERT auth.users membuat profil dalam transaksi yang sama. Nama valid diambil dari raw_user_meta_data.display_name, dipangkas dan dibatasi 100 karakter; nilai hilang, non-string atau kosong memakai "Pengguna". Nama bukan atribut otorisasi. Fungsi SECURITY DEFINER memakai search_path kosong, nama objek berkualifikasi, dan EXECUTE tidak diberikan kepada PUBLIC. Backfill memakai aturan nama yang sama dan ON CONFLICT DO NOTHING.

RLS aktif:
- anon tidak memiliki akses.
- authenticated hanya SELECT/UPDATE profil dengan (select auth.uid()) = id.
- UPDATE memakai USING serta WITH CHECK dan hanya mendapat privilege kolom display_name.
- INSERT/DELETE tidak diberikan kepada authenticated. Pembuatan profil melalui trigger.
- updated_at diperbarui oleh trigger, bukan input client.

Runtime Drizzle memakai login database khusus, misalnya livetranslate_runtime, tanpa SUPERUSER/BYPASSRLS dan bukan owner tabel. Login tersebut diberi keanggotaan role authenticated. Pada setiap query profil, satu transaksi memasang role authenticated dan request.jwt.claims menggunakan set_config(..., true), lalu SELECT profil dengan id dari hasil verifikasi token. set_config dan query harus memakai koneksi transaksi yang sama. Klaim minimal sub dan role dibentuk server dari identitas terverifikasi. Tidak menerima user_id atau klaim database dari body client.

Migrasi dapat membuat role NOLOGIN dengan privilege minimum; pemberian LOGIN/password dilakukan terpisah oleh operator. Tidak menyimpan password role dalam SQL/version control. DATABASE_URL runtime dan MIGRATION_DATABASE_URL berprivilege dipisahkan.

## Kontrak API publik
Semua endpoint di bawah /api/auth. JSON memakai snake_case. Respons diberi Cache-Control: no-store. Password dan token tidak dicatat di log.

| Endpoint | Input | Hasil |
| --- | --- | --- |
| POST /register | display_name, email, password | 202 dan pesan generik untuk memeriksa email; jika Supabase langsung mengembalikan sesi, 201 dengan user/session |
| POST /login | email, password | 200 dengan user/session |
| POST /refresh | refresh_token | 200 dengan sesi pengganti |
| POST /logout | Bearer access token | 204 setelah logout sesi saat ini |
| GET /me | Bearer access token | 200 dengan user dan profile |

Session hanya memuat access_token, refresh_token, expires_at dan token_type. User publik hanya id dan email; profile memuat empat kolom tabel. Respons error: {"error":{"code":"...","message":"..."}}.

Validasi:
- JSON object saja, Content-Type application/json untuk request berbody; tolak field yang tidak dikenal.
- Body maksimum 16 KiB.
- display_name dipangkas; 1–100 code point Unicode. Karakter berbahaya tetap ditampilkan sebagai teks oleh React.
- Email dipangkas dan diperiksa format/panjang maksimum 254 karakter.
- Password tidak dipangkas; 8–128 code point, dengan kebijakan Supabase yang lebih ketat tetap berlaku.
- Token wajib string tidak kosong dan dibatasi ukuran body/header.
- 400 input tidak valid; 401 login/token ditolak; 413 body terlalu besar; 415 tipe konten salah; 429 rate limit; 502/504 provider gagal/timeout; 503 konfigurasi belum siap.
- Pesan login salah dan email belum dikonfirmasi sama-sama generik agar tidak membocorkan status akun. Pesan signup untuk akun lama mengikuti perilaku anti-enumerasi Supabase.
- Detail SQL, stack trace dan respons mentah provider tidak diteruskan ke FE.

Go hanya memproxy daftar metode/path yang diketahui dan header yang diperlukan. AUTH_SERVICE_URL ditetapkan konfigurasi server, bukan input client. Header X-Forwarded-For dari client tidak dipercaya.
Rate limit Go per RemoteAddr: maksimal 10 request register/login per 60 detik gabungan, dengan jumlah entri dibatasi dan cleanup kedaluwarsa. Ini batas single-instance untuk deployment awal; Supabase tetap menerapkan batas providernya.

## Lifecycle sesi dan FE
Popup memiliki mode Masuk/Daftar. Form daftar berisi nama, email, password dan konfirmasi password; konfirmasi hanya diperiksa FE dan tidak dikirim. UI berbahasa Indonesia, field memiliki label, tombol dinonaktifkan selama request, dan pesan status dapat dibaca screen reader.

Setelah login, GET /me memvalidasi identitas dan mengambil profil. Tampilkan nama, email, tombol Keluar, serta pemeriksaan backend yang sudah ada.
Token disimpan pada chrome.storage.session, akses TRUSTED_CONTEXTS saja; manifest menambah permission storage. Jangan menyimpan password. Pada mode pengembangan browser biasa, fallback hanya memori.

Saat popup dibuka, baca sesi. Refresh jika mendekati kedaluwarsa (60 detik), lalu panggil /me. Pada 401 coba refresh paling banyak sekali; simpan token pengganti dan hindari refresh paralel. Tidak mengulang refresh pada kegagalan jaringan dengan token lama secara agresif. Kegagalan jaringan dibedakan dari sesi tidak valid; sesi lokal dihapus setelah penolakan token definitif. Logout selalu membersihkan sesi lokal; jika provider gagal UI menyatakan sesi lokal sudah dihapus tetapi logout remote belum terkonfirmasi.

Konfirmasi email aktif direkomendasikan. Pengguna mengonfirmasi link email lalu membuka extension dan login; tidak menambahkan callback extension atau session dari URL. SUPABASE_AUTH_REDIRECT_URL memakai HTTPS milik deployment, dengan pengecualian HTTP localhost/127.0.0.1/[::1] untuk pengembangan lokal, dan masuk allowlist Supabase; halaman landing minimum menampilkan instruksi kembali ke extension tanpa memproses token. Konfigurasi deployment ini didokumentasikan dan diperiksa manual, bukan membuat domain/cloud baru.

## Konfigurasi dan deployment
Go: AUTH_SERVICE_URL, AUTH_INTERNAL_TOKEN; port host mengikuti APP_PORT yang sudah ada.
Auth-service: AUTH_INTERNAL_TOKEN, SUPABASE_URL, SUPABASE_PUBLISHABLE_KEY, DATABASE_URL, SUPABASE_AUTH_REDIRECT_URL.
Tool migrasi: MIGRATION_DATABASE_URL.
Kredensial database hanya diteruskan ke auth-service/tool migrasi. Kunci Deepgram/Sumopod hanya diteruskan ke Go. Jangan memuat semua isi .env ke setiap container tanpa kebutuhan.

SSL PostgreSQL wajib dengan validasi sertifikat; postgres.js memakai prepare:false untuk kompatibilitas transaction pooler. Konfigurasi rahasia kosong/tidak valid menyebabkan endpoint terkait gagal aman, tanpa mematikan liveness. Produksi memakai HTTPS untuk API publik dan Supabase.
Selaraskan VITE_API_BASE_URL, APP_PORT dan host_permissions extension; saat ini konfigurasi port 8080/8989 tidak konsisten.
Pemeriksaan -check supabase pada Go diarahkan ke endpoint internal pemeriksaan Drizzle (SELECT 1), sehingga pgx tidak lagi dipakai untuk koneksi database. Pemeriksaan ini tidak memerlukan Supabase Auth, tidak menulis data, dan wajib gagal nonzero bila koneksi gagal.

## Migrasi
Folder migration/ pada root workspace menyimpan SQL dan metadata Drizzle. Definisi schema di auth-service/src/db/schema.ts; auth-service/drizzle.config.ts menunjuk output ../migration dan kredensial MIGRATION_DATABASE_URL.

Migrasi mencakup tabel, constraint, RLS/grants, role runtime tanpa password, trigger dan backfill. Gunakan dukungan referensi auth.users yang sudah ada dan custom SQL untuk objek yang tidak dihasilkan schema. Review output agar tidak membuat ulang schema auth.
Generate dan apply adalah perintah terpisah. Tidak menjalankan schema push atau migrasi otomatis saat startup. Uji pada database terpisah sebelum apply ke Supabase tujuan. Migrasi tidak memasukkan secrets atau menggunakan kredensial runtime untuk DDL.

## Verifikasi penerimaan
1. Unit test auth-service memakai provider/repository palsu: validasi, error provider, sesi, token ditolak dan profil berdasarkan identitas terverifikasi.
2. Test Go memakai httptest: allowlist route, secret internal, limit body, rate limit, timeout, no-store dan tidak membocorkan error.
3. Test FE: penyimpanan sesi, refresh tunggal, rotasi token, 401 definitif, kegagalan jaringan dan logout lokal.
4. Uji database opt-in memakai database uji Supabase yang ditentukan eksplisit: migrasi dua kali (kedua no-op), backfill akun lama, trigger signup, default nama, RLS isolasi dua akun, anon ditolak, role runtime tidak bypass RLS, proteksi kolom dan rollback saat trigger gagal.
5. Build TypeScript auth-service, npm build extension, go test/vet, sintaks Compose, dan build container jika akses daemon tersedia.
6. Manual Chrome: popup ditutup/dibuka tetap login; browser restart meminta login; alur verifikasi email dan kondisi provider/backend mati.
7. Laporkan pemeriksaan yang tidak dapat dijalankan sebagai BELUM DIUJI, bukan lulus. Tidak menghubungi provider AI berbayar dalam tugas autentikasi.

## Referensi dokumentasi
Dokumentasi Supabase/Drizzle telah ditelusuri melalui Context7 pada tahap usulan.
- https://orm.drizzle.team/docs/get-started/supabase-new
- https://orm.drizzle.team/docs/drizzle-config-file
- https://orm.drizzle.team/docs/rls
- https://supabase.com/docs/guides/auth/managing-user-data
- https://supabase.com/docs/guides/auth/passwords
- https://supabase.com/docs/guides/database/postgres/row-level-security

