# LiveTranslate — Feature Roadmap & Katalog Fitur

> Dokumen kanonis arah produk, katalog fitur, status implementasi, dependensi, dan kriteria selesai.
>
> **Aturan agent:** baca dokumen ini sebelum merancang atau mengimplementasikan fitur apa pun. Setelah pekerjaan mengubah status, scope, dependensi, keputusan, atau hasil verifikasi, perbarui dokumen ini pada **sesi yang sama**. Jangan menandai fitur selesai berdasarkan kode saja;lampirkan bukti sesuai definisi status dan kriteria selesai.
>
> **Satu sumber kebenaran:** roadmap ini adalah daftar fitur dan statusnya. Detail desain, plan, dan hasil uji tetap berada pada dokumen khusus (`docs/implementation/`, `docs/superpowers/`, `docs/obsidian-pending/`) dan hanya diringkas di sini.

Terakhir diperbarui: 2026-10-24 (paket P0 diimplementasikan lokal; validasi Chrome unpacked, Supabase target, bahasa/provider nyata, latensi, dan restore tetap gate produksi)  
Sumber arah: implementasi repository, PRD disetujui, paket eksekusi, hasil harness PostgreSQL/race/build/container, smoke popup Chromium terkelola, dan keputusan pengguna.

---

## 1. Arah aplikasi

LiveTranslate adalah **Chrome extension pendamping meeting berbasis browser**. Pengguna login, extension mengenali konteks meeting, menangkap audio yang diizinkan, mengubah percakapan menjadi transkrip dan terjemahan, lalu memberi bantuan berbasis konteks meeting tanpa mencampur data antar pengguna atau antar meeting.

### Amandemen kebutuhan produk — brainstorming 2026-10-24

Pengguna menetapkan pengalaman target: extension otomatis menawarkan koneksi saat masuk meeting; percakapan dari beragam bahasa diterjemahkan ke **bahasa Indonesia** dan dicatat; catatan menjadi konteks pemetaan pertanyaan yang perlu dijawab pengguna; dokumen unggahan pengguna menjadi sumber jawaban otomatis dengan kutipan yang dapat diperiksa. Agent dengan beberapa iterasi pemolesan adalah kemungkinan desain, belum keputusan arsitektur.

Kebutuhan ini sekarang menjadi acuan implementasi setelah pengguna memberi perintah eksekusi pada 2026-10-24. Bagian katalog di bawah tetap memuat baseline dan keputusan yang belum selesai; bila bertentangan tentang arah target, amandemen ini berlaku. Plan lama tidak boleh dieksekusi untuk mengembalikan arsitektur yang telah ditinggalkan.

- Prioritas saat ini: **validasi lingkungan nyata**, bukan penambahan fitur—terapkan migration target, uji Chrome unpacked/tab audio, jalankan matriks bahasa Deepgram/Sumopod, ukur p95 tiga meeting, lalu drill backup/restore.
- Hanya audio peserta dari tab meeting yang ditangkap; extension tidak meminta mikrofon. Capture tetap memerlukan klik eksplisit setelah tawaran koneksi.
- Catatan/terjemahan tampil live. Sesudah meeting, paket lengkap dapat disimpan tanpa expiry otomatis atau dibuang; staging tanpa keputusan dibersihkan setelah 24 jam.
- Bahasa tujuan Indonesia; dukungan bahasa sumber berstatus PERLU VALIDASI sampai matriks akun/provider nyata dijalankan.
- Jawaban otomatis tidak dikirim/disuarakan ke meeting. Jawaban Indonesia+Inggris hanya dari dokumen occurrence dan wajib citation; tanpa bukti kedua versi `-`.
- Upload/index/citation memakai PDF text/OCR, DOCX, XLSX cached values, CSV, lexical retrieval owner+occurrence, dan manifest arsip schema v2. Embedding/vector DB tetap di luar P0 sampai evaluasi membuktikan kebutuhan.

Detail kebutuhan, asumsi penulis dan pertanyaan: [brainstorm penyelarasan planning](../obsidian-pending/2026-10-24-planning-realignment-brainstorm.md).

### 1.1 Alur produk target

```text
Login
  → kenali meeting dan meeting_id
  → persetujuan capture
  → tangkap audio meeting
  → transkripsi streaming
  → terjemahan langsung
  → simpan artefak per meeting
  → deteksi pertanyaan / permintaan bantuan
  → susun konteks meeting yang terisolasi
  → hasilkan jawaban atau ringkasan
  → tampilkan dan ekspor history meeting
```

### 1.2 Nilai utama yang harus dipertahankan

| Nilai | Konsekuensi teknis |
| --- | --- |
| Isolasi data meeting | Setiap artefak terikat `user_id` + identitas meeting; RLS dan verifikasi backend berlapis |
| Privasi'| Tidak ada capture/rekaman tanpa izin eksplisit; secret tidak pernah di extension |
| Keandalan offline | Cache/outbox tanpa token, retry berkode, tidak ada data meeting yang hilang diam-diam |
| Batas biaya | Streaming dan LLM harus punya batas token, timeout, dan fallback |
| Progresif | Setiap fase dapat dipakai dan diuji sendiri tanpa fase berikutnya |

### 1.3 Prinsip yang dipakai dalam katalog

1. Setiap fitur punya ID stabil. ID tidak pernah dipakai ulang, bahkan jika fitur dibatalkan; status menjadi `DI LUAR BATAS`.
2. Setiap fitur punya kriteria selesai yang bisa diverifikasi.
3. Fitur yang bergantung pada keputusan produk ditandai `TERBLOKIR KEPUTUSAN` sampai keputusannya tercatat di Bagian 9.
4. Fitur yang masih memerlukan bukti pada service/browser nyata ditandai `PERLU VALIDASI`, bukan `SELESAI LOKAL`.
5. Skema ID fitur: huruf domain + nomor urut (`A-01`, `B-01`, … `N-01`; domain keamanan memakai `S-01`…`S-12`). Skema ID keputusan: `DEC-01`…`DEC-18`. Kedua skema tidak boleh berbagi token yang sama.

---

## 2. Definisi status dan prioritas

### 2.1 Status

| Status | Arti | Bukti minimum |
| --- | --- | --- |
| `SELESAI LOKAL` | Kode, test, build, dan smoke path terkait lulus secara lokal | Perintah verifikasi dijalankan pada sesi ini + ringkasan hasil |
| `PERLU VALIDASI` | Implementasi tersedia, acceptance end-to-end pada service/browser nyata belum terbukti | Test lokal lulus + checklist validasi manual yang belum dijalankan |
| `SIAP DIRANCANG` | Arah dan dependensi cukup jelas untuk mulai desain, belum disetujui sebagai spec | Draft desain + daftar keputusan terbuka |
| `TERBLOKIR KEPUTUSAN` | Perubahan produk/privasi/biaya; butuh keputusan pengguna | Nomor keputusan terbuka yang mengunci |
| `BELUM DIMULAI` | Belum ada implementasi produk | — |
| `DI LUAR BATAS` | Tidak dikerjakan tanpa perubahan arah eksplisit | Alasan batas dicatat |

`SELESAI LOKAL` **tidak** sama dengan siap produksi.

### 2.2 Prioritas

| Prioritas | Arti |
| --- | --- |
| `P0` | Memblokir alur nilai utama; harus ada sebelum rilis |
| `P1` | Menentukan kegunaan harian setelah alur utama jalan |
| `P2` | Peningkatan kualitas, jangkauan, atau operasional |

---

## 3. Batas arsitektur yang berlaku

Batas ini berlaku sampai pengguna mengubahnya secara eksplisit.

**Skala target terbaru:** pengguna menetapkan tim terbatas, bahkan mungkin di bawah tiga orang; bukan layanan signup publik. Pengguna menegaskan **setiap anggota hanya boleh mengakses miliknya sendiri**, tanpa sharing tim. Rekomendasi desain: satu backend modular dengan batas concurrency/resource; margin uji tiga meeting bersamaan adalah usulan penulis, bukan target yang sudah disetujui.

**Desain konsolidasi disetujui dan eksekusi diotorisasi:** [PRD dan penyelarasan produk 2026-10-24](../superpowers/specs/2026-10-24-product-realignment-design.md). Larangan implementasi pada sesi planning telah digantikan oleh instruksi eksplisit pengguna untuk mengeksekusi paket P0. Persetujuan tetap tidak membuktikan target performa/provider; status hanya dinaikkan setelah bukti yang diwajibkan tersedia.

**Wajib dijaga**

- Extension: React + Vite **JavaScript**, Manifest V3, tanpa framework UI tambahan, tanpa state manager.
- Backend: Go, standard library bila memadai; tanpa framework Go dan tanpa SDK LLM tambahan.
- Data: Supabase Auth (identitas) dan Supabase Data API/PostgREST (tabel aplikasi + RLS). Tidak ada koneksi database langsung dari backend.
- Provider: Deepgram untuk transkripsi, Sumopod untuk LLM. Host dan model diambil dari konfigurasi nyata, tidak ditebak.
- Secret hanya di backend. Tidak pernah di extension, build artifact frontend, log, fixture, atau repository.
- Satu service backend pada Compose. Tidak ada Redis, queue, vector database, atau service AI lokal.
- Tidak ada request berbayar otomatis saat startup atau test biasa.
- Tidak ada rekaman audio tersimpan tanpa izin eksplisit dan kebijakan retensi yang sudah diputuskan.
- Semua fitur meeting memakai identitas meeting yang diverifikasi backend; client tidak boleh menentukan `user_id`.

**Dilarang tanpa desain disetujui**

- Menambah dependency runtime baru.
- Mock, stub, no-op, atau fallback palsu untuk menutupi provider yang belum siap.
- Melewati dependency dengan membuat tabel/kolom sebagai bypass RLS.

**Catatan kondisi workspace**

- `live_translate/` (implementasi lama) tidak ditemukan pada pemeriksaan 2026-10-21. Jangan direkonstruksi tanpa sumber; laporkan ke pengguna.

---

## 4. Ringkasan fase

| Fase | Cakupan | Status fase | Keluaran |
| --- | --- | --- | --- |
| F0 | Fondasi runtime | SELESAI LOKAL | Backend Go, extension MV3, Compose, health, mode check |
| F1 | Identitas dan akun | PERLU VALIDASI | Auth, profile, session, RLS. Live: login/refresh/logout/me lulus; sisa signup happy path (rate limit email project) dan uji Chrome |
| F2 | Konteks meeting | PERLU VALIDASI | Deteksi URL, `meeting_id`, sinkronisasi offline. Live: resolve/list/detail, idempotensi, race, dan isolasi akun lulus; sisa uji Chrome nyata |
| F3 | Capture audio | SELESAI LOKAL | Stream audio terikat meeting, izin eksplisit, tanpa penyimpanan audio |
| F4 | Transkripsi streaming | PERLU VALIDASI | Segmen transkrip partial/final. Implementasi lokal lulus (104/104 test extension, semua paket Go lulus, build lulus) dan jalur server terbukti live 2026-10-22 (run/chunk/stop/SSE dengan key Deepgram nyata, 3 partial + 1 final dari audio sampel); validasi manual Chrome dan streaming dari browser belum dijalankan |
| F5 | Terjemahan | PERLU VALIDASI | Implementasi lokal final→Indonesia selesai; model, matriks bahasa, latency, dan provider nyata belum divalidasi |
| F6 | Penyimpanan & timeline | PERLU VALIDASI | Occurrence staging, keputusan pascameeting, arsip permanen, history keyset, detail, export, delete tersedia lokal; cloud/native Chrome belum divalidasi |
| F7 | Assistance AI | PERLU VALIDASI | Pertanyaan otomatis dan jawaban bilingual evidence-only tersedia lokal; evaluasi model/corpus nyata belum |
| F8 | Dokumen & retrieval/RAG | PERLU VALIDASI | PDF/DOCX/XLSX/CSV, OCR, locator, lexical retrieval, citation verification tersedia lokal; Storage/corpus nyata belum |
| F9 | History & ringkasan | PERLU VALIDASI | History arsip, detail, export, dan delete tersedia lokal; ringkasan meeting di luar batas P0 |
| F10 | Hardening & rilis | PERLU VALIDASI | Hardening keamanan, bounded resources, observabilitas, image, dan runbook selesai lokal; gate cloud/Chrome/provider/restore/soak belum |

---

## 5. Katalog fitur

Format tiap fitur: **ID — Nama** · prioritas · status · dependensi · acceptance.

### A. Platform dan fondasi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| A-01 | Backend Go dengan `net/http` dan routing per fitur | P0 | SELESAI LOKAL | `go build ./cmd/server` lulus; route `/health`, `/api/auth/*`, `/api/meetings*` terdaftar |
| A-02 | `GET /health` liveness tanpa dependensi eksternal | P0 | SELESAI LOKAL | 200 + `{"status":"ok","service":"livetranslate-backend"}` tanpa kredensial |
| A-03 | Konfigurasi environment + validasi | P0 | SELESAI LOKAL | Config wajib menolak nilai kosong; publishable/anon key diterima, secret/service-role ditolak |
| A-04 | Timeout berlapis | P0 | SELESAI LOKAL | Koneksi 10s, AI 30s, enforced di setiap client HTTP |
| A-05 | Dockerfile multi-stage, runtime non-root + CA | P0 | SELESAI LOKAL | Build image lulus; container berjalan tanpa root dan dapat menghubungi HTTPS |
| A-06 | Compose satu service backend, port loopback | P0 | SELESAI LOKAL | `docker compose config --quiet` lulus; hanya service `backend`; bind `127.0.0.1` |
| A-07 | Mode CLI check provider | P0 | SELESAI LOKAL | `-check deepgram` dan `-check sumopod` exit 0 hanya bila sukses |
| A-08 | Mode CLI check Supabase | — | DI LUAR BATAS | Dihapus saat migrasi ke Supabase HTTP API; akses diperiksa lewat alur auth |
| A-09 | Extension Vite MV3 build | P0 | SELESAI LOKAL | `npm run build` menghasilkan `dist/background.js` module, popup, manifest |
| A-10 | Popup pemeriksaan backend | P1 | SELESAI LOKAL | Status tampil saat backend hidup dan gagal saat mati |
| A-11 | Fixture audio non-sensitif untuk check Deepgram | P1 | SELESAI LOKAL | `fixtures/check.wav` ada dan(check) lulus dengan key nyata. Berkas diabaikan Git; pada 2026-10-22 dipakai sampel SDK Deepgram (suara manusia, bukan data pengguna) dan `docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav` menghasilkan `deepgram: LULUS` |
| A-12 | Kompresi dan garbage collection build | P2 | BELUM DIMULAI | Artefak build reproducible dan kecil |

### B. Identitas dan akun

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| B-01 | Register lewat Supabase Auth | P0 | PERLU VALIDASI | 201 bila sesi ada, 202 bila konfirmasi email diperlukan; profil dibuat trigger. Live: pemetaan error terbukti (upstream 400 → `registration_failed`, 429 → `rate_limit`); signup sukses tertahan rate limit email project |
| B-02 | Login email/password | P0 | SELESAI LOKAL | 200 + `{user, session}`; kredensial salah ditolak tanpa membocorkan detail |
| B-03 | Refresh token | P0 | SELESAI LOKAL | Token dirotasi dan disimpan; refresh paralel hanya mengulang sekali |
| B-04 | Logout lokal | P0 | SELESAI LOKAL | Scope lokal; state lokal terhapus walau backend mati |
| B-05 | `GET /api/auth/me` | P0 | SELESAI LOKAL | `{user, profile}`; profil diambil lewat Data API dengan JWT pengguna |
| B-06 | Sesi di `chrome.storage.session` | P0 | SELESAI LOKAL | `TRUSTED_CONTEXTS`; token tidak terbaca dari halaman web |
| B-07 | Refresh otomatis sebelum kedaluwarsa | P0 | SELESAI LOKAL | Refresh saat `expires_at` < 60 detik; kegagalan 401 membersihkan sesi |
| B-08 | Error contract bertingkat | P0 | SELESAI LOKAL | Kode: `supabase_config`, `auth_api_error`, `profile_api_error`, `profile_missing`, `unauthorized`, timeout |
| B-09 | Rate limit auth proses lokal | P1 | SELESAI LOKAL | Permintaan berlebih dari sumber sama dibatasi |
| B-10 | `no-store` pada semua response auth | P1 | SELESAI LOKAL | Header `Cache-Control: no-store` pada seluruh endpoint auth |
| B-11 | Migrasi `0001_supabase_data_api.sql` | P0 | SELESAI LOKAL | Terapkan di Supabase nyata; tabel, trigger, grants, backfill, RLS aktif |
| B-12 | Tampilan profil dan edit `display_name` | P2 | PERLU VALIDASI | Popup menampilkan nama/email (terbukti live); endpoint update `display_name` belum ada di backend |
| B-13 | Multi-profil / ganti akun | P2 | BELUM DIMULAI | Pengguna dapat berpindah akun tanpa mencampur state meeting |
| B-14 | Pemulihan akun (reset password) | P2 | BELUM DIMULAI | Alur lupa password via Supabase; token tetap di `storage.session` |
| B-15 | Onboarding pengguna baru | P2 | BELUM DIMULAI | Langkah pertama jelas: login, izin, Meeting pertama |

### C. Konteks meeting dan siklus sesi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| C-01 | Deteksi URL tab aktif tanpa popup | P0 | PERLU VALIDASI | Deteksi pada aktivasi tab, perubahan URL/status, fokus window, tanpa membuka popup |
| C-02 | Adapter URL Google Meet | P0 | PERLU VALIDASI | Kode `abc-defg-hij` dikenali; homepage ditolak |
| C-03 | Adapter URL Zoom Web App | P0 | PERLU VALIDASI | `/wc/{id}/join`, `/wc/{id}/start`, `/wc/join/{id}`, `/wc/start/{id}`; launcher `/j/` ditolak |
| C-04 | Adapter URL Jitsi `meet.jit.si` | P0 | PERLU VALIDASI | Room ASCII dinormalisasi lowercase; path bertingkat ditolak |
| C-05 | Penolakan host/path tidak didukung dan domain tiruan | P0 | SELESAI LOKAL | Fixture 30+ URL: hanya yang didukung diterima |
| C-06 | Pembuangan query/fragment/passcode | P0 | SELESAI LOKAL | Canonical URL tidak memuat parameter non identitas |
| C-07 | Canonical URL deterministik | P0 | SELESAI LOKAL | Alih path/host menghasilkan satu `meeting_key` |
| C-08 | `POST /api/meetings/resolve` idempoten | P0 | SELESAI LOKAL | Insert atomik via unique constraint; paralel menghasilkan satu `meeting_id` |
| C-09 | `GET /api/meetings` daftar milik pengguna | P0 | SELESAI LOKAL | Hanya baris pemilik; limit 1–100 |
| C-10 | `GET /api/meetings/{id}` detail | P0 | SELESAI LOKAL | 404 untuk sesi hilang atau milik akun lain |
| C-11 | Verifikasi Bearer token via Supabase Auth | P0 | SELESAI LOKAL | Token tak valid ditolak sebelum akses database |
| C-12 | Recalculate identity di backend | P0 | SELESAI LOKAL | Backend menghitung ulang `meeting_key`; key client tidak dipercaya |
| C-13 | RLS owner-only `meeting_sessions` | P0 | SELESAI LOKAL | Anon dan akun lain tidak dapat read/insert |
| C-14 | Migrasi `0002_meeting_sessions.sql` | P0 | SELESAI LOKAL | Terapkan setelah 0001; idempoten; unique identity |
| C-15 | Cache persisten tanpa token | P0 | SELESAI LOKAL | `chrome.storage.local` menyimpan `meeting_key → meeting_id` tanpa credential |
| C-16 | Outbox offline + retry backoff | P0 | SELESAI LOKAL | Alarm 1 menit; backoff 1–30 menit; tidak ada duplikasi |
| C-17 | Isolasi state antar akun | P0 | SELESAI LOKAL | Logout/pergantian akun menghapus cache, outbox, dan pemetaan lama |
| C-18 | Proteksi race tab/respons | P0 | SELESAI LOKAL | Respons lama tidak menimpa tab/akun baru; ticket/generation |
| C-19 | Popup status sinkronisasi | P1 | PERLU VALIDASI | Status unsupported/no_active_tab/login_required/syncing/registered/sync_error |
| C-20 | `meeting_run_id` per occurrence | P1 | SELESAI LOKAL | `meeting_id` tetap identitas link; occurrence baru dibuat setelah **Terhubung**, dan setiap capture run wajib memiliki `occurrence_id`; backfill lama satu run:satu occurrence |
| C-21 | Deteksi joined-state via DOM/content script | P1 | TERBLOKIR KEPUTUSAN | Keputusan DEC-02; status presence akurat per provider |
| C-22 | Rekonsiliasi sesi tersisa | P2 | TERBLOKIR KEPUTUSAN | Kebijakan saat browser crash tanpa event akhir |
| C-23 | Multi-tab bukan multi-run | P1 | PERLU VALIDASI | Tab kedua dengan link sama memakai `meeting_id` sama (perilaku saat ini disepakati) |
| C-24 | Dukungan provider tambahan | P2 | BELUM DIMULAI | Adapter baru + fixture URL + aturan normalisasi terverifikasi |
| C-25 | Self-hosted Jitsi | P2 | BELUM DIMULAI | Allowlist domain eksplisit, bukan wildcard |

### D. Capture audio

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| D-01 | Sumber audio tab meeting | P0 | SELESAI LOKAL | Audio tab meeting saja; mikrofon pengguna tidak diakses, izin `audioCapture` tidak dipakai |
| D-02 | Persetujuan pengguna eksplisit | P0 | SELESAI LOKAL | Tidak ada capture sebelum pengguna menekan "Mulai merekam" di popup; worker menolak capture tanpa meeting terdaftar |
| D-03 | Indikator status capture | P0 | SELESAI LOKAL | Popup menampilkan status capture, run ID, jumlah chunk, dan total byte |
| D-04 | Lifecycle start/stop deterministik | P0 | SELESAI LOKAL | Capture berhenti saat tombol Berhenti ditekan, tab meeting ditutup, pengguna logout, transkripsi gagal, atau backend menolak run secara terminal (409/502 atau token yang tidak dapat diperbarui). Kesalahan jaringan sementara tidak menghentikan capture: audio berlubang dan capture berlanjut. Menutup popup tidak menghentikan capture |
| D-05 | Streaming panjang via offscreen document | P0 | SELESAI LOKAL | `chrome.tabCapture.getMediaStreamId` + dokumen offscreen `USER_MEDIA`; service worker tidak memegang stream |
| D-06 | Audio terikat `meeting_id` | P0 | SELESAI LOKAL | Worker yang memilih meeting; run terikat `meeting_id` terverifikasi, tab lain tidak tertukar |
| D-07 | Normalisasi format/frame audio | P0 | SELESAI LOKAL | Capture PCM16 mono dari `AudioWorklet` (bukan container webm/opus: container webm yang dialirkan tidak pernah menghasilkan segmen final di Deepgram). Frame 100 ms dikirim sebagai biner; sample rate run diteruskan sebagai `encoding=linear16` |
| D-08 | Backpressure dan antrean terbatas | P0 | SELESAI LOKAL | Antrean upload satu per satu; chunk diproses berurutan, tidak menumpuk |
| D-09 | Error capture terisolasi | P1 | SELESAI LOKAL | Kegagalan capture tidak merusak state meeting; status error tampil di popup |
| D-10 | Mikrofon pengguna | P2 | DI LUAR BATAS | Keputusan DEC-04: sumber audio dibatasi ke tab meeting; perlu perubahan arah eksplisit |
| D-11 | Tanpa penyimpanan audio mentah | P0 | SELESAI LOKAL | Byte audio diukur lalu dibuang; hanya metadata run yang tersimpan |
| D-12 | Indikator presence audio | P2 | BELUM DIMULAI | Feedback visual saat suara terdengar (tergantung D-01) |

### E. Transkripsi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| E-01 | Deepgram streaming via backend | P0 | SELESAI LOKAL | Key hanya di backend; stream autentik dan punya ownership check. Terbukti live 2026-10-22 di container nyata: run 201 → 2 chunk (0 → 32.000 → 74.880 byte) → SSE 3 partial + 1 final + `end` → stop 200 `stopped`, dengan key Deepgram nyata; run akun lain 404. Jalur browser tetap pada validasi Chrome (M-07) |
| E-02 | Segmen partial/final | P0 | SELESAI LOKAL | Segmen punya sequence, timestamp, status; final tidak berubah diam-diam. Test Go broker dan test parser SSE extension lulus |
| E-03 | Reconnect tanpa duplikasi | P0 | BELUM DIMULAI | Reconnect tidak menggandakan segmen final. Di luar cakupan F4: desain F4 tidak mencakup reconnect otomatis ke Deepgram |
| E-04 | Cancellation dan timeout | P0 | SELESAI LOKAL | Stream punya batas waktu dan dapat dibatalkan: deadline write provider, anggaran tutup sesi, dan route stop idempoten; test timeout dan idempotensi lulus |
| E-05 | Error provider tanpa kebocoran | P0 | SELESAI LOKAL | Credential dan body audio tidak masuk log/response; event `error` SSE hanya berisi kode dan pesan generik |
| E-06 | Pemisahan segmen per meeting | P0 | SELESAI LOKAL | Segmen meeting A tidak masuk meeting B: broker dipetakan per run, dan run dibaca dengan token pemanggil sehingga RLS menyembunyikan run akun lain |
| E-07 | Model/bahasa dari konfigurasi nyata | P0 | SELESAI LOKAL | `DEEPGRAM_MODEL=nova-2` dan `DEEPGRAM_LANGUAGE=multi`; keduanya diuji terhadap akun nyata. `2-general` dan `2-meeting` ditolak akun, `multi`/`en-US`/`id` diterima |
| E-08 | Check prerecorded Deepgram | P1 | SELESAI LOKAL | `-check deepgram` lulus dengan fixture dan key nyata: `docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav` → `deepgram: LULUS` (2026-10-22) |
| E-09 | Sinkronisasi subtitle real-time | P1 | PERLU VALIDASI | Teks berjalan mengikuti audio: segmen masuk ke popup dari state `chrome.storage.session` dan tampil berurutan. Latensi belum diukur di browser nyata karena tidak ada binary Chrome di lingkungan ini |
| E-10 | Speaker diarization | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-06; label speaker tidak boleh disalahartikan sebagai identitas nyata |

### F. Terjemahan

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| F-01 | Terjemahan segmen live ke Indonesia | P0 | PERLU VALIDASI | Final memicu Sumopod non-stream, disimpan dan tampil sejajar; model/bahasa nyata serta p95 belum divalidasi |
| F-02 | Deteksi bahasa sumber beragam | P0 | PERLU VALIDASI | Bahasa Deepgram diteruskan ke prompt; matriks bahasa dan code-switching nyata belum dijalankan |
| F-03 | Bahasa tujuan Indonesia | P0 | SELESAI LOKAL | Output translation dikunci `language='id'`; tidak ada selector multi-target |
| F-04 | Referensi segmen sumber pada terjemahan | P0 | SELESAI LOKAL | FK `source_segment_id`, run/seq/revision, owner+occurrence diperiksa migration0007 dan harness RLS |
| F-05 | Perilaku koreksi sumber | P1 | PERLU VALIDASI | Schema mendukung `source_revision`; baseline final immutable belum menghasilkan revisi provider nyata |
| F-06 | Latensi terjemahan | P1 | PERLU VALIDASI | Queue bounded dan timeout15detik; target p95≤3detik belum diukur pada tiga meeting |
| F-07 | Batas biaya terjemahan | P1 | PERLU VALIDASI | Satu request non-stream per final, queue128 dan timeout; quota/rate akun nyata belum diuji |
| F-08 | Glosarium istilah | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-09 |

### G. Penyimpanan dan timeline

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| G-01 | Skema `meeting_runs` | P0 | SELESAI LOKAL | `meeting_runs` terpasang live: id, user_id, meeting_id, status, bytes/chunks, started_at, ended_at; RLS owner-only; fungsi atomik untuk akuntansi |
| G-02 | Skema `transcript_segments` | P0 | SELESAI LOKAL | Migration0004 dan invoker RPC: final immutable, owner/meeting/run konsisten, unique seq, duplicate identik idempoten, konflik ditolak. PostgreSQL18.6 + PostgREST16.5 lokal dan harness RLS/reapply/concurrency lulus; target cloud belum diterapkan |
| G-03 | Skema `translations` | P0 | SELESAI LOKAL | Migration0007 idempoten; source FK, revision, status success/failed, target Indonesia, RLS owner-only |
| G-04 | RLS seluruh tabel meeting | P0 | PERLU VALIDASI | Harness lokal membuktikan occurrence, translation, documents/chunks, questions/answers, archives lintas dua owner; migration target cloud/Storage policy belum diterapkan |
| G-05 | Endpoint append/list segmen | P0 | SELESAI LOKAL | Final server-generated, translation list current-run, serta snapshot/keyset timeline lama; foreign owner ditolak |
| G-06 | Retensi data | P0 | SELESAI LOKAL | Target baru: staging `awaiting_decision`24jam; job lama dinonaktifkan; arsip committed tidak dipurge berdasarkan umur |
| G-07 | Hapus/export pengguna | P0 | PERLU VALIDASI | List keyset owner-only, detail manifest+metadata, export ZIP paginated memverifikasi hash/count DB dan SHA-256 sumber; delete menyimpan tombstone lalu durable Storage API cleanup. Storage target/ZIP nyata belum divalidasi |
| G-08 | Timeline meeting | P1 | PERLU VALIDASI | Popup menampilkan run lama dan daftar archive occurrence; smoke Chromium terkelola lulus, Chrome unpacked tetap gate |
| G-09 | Embedding untuk retrieval | P2 | DI LUAR BATAS | P0 memakai PostgreSQL lexical retrieval; embedding hanya bila evaluasi representatif membuktikan kebutuhan |
| G-10 | Indeks dan re-index | P2 | SELESAI LOKAL | GIN `tsvector` simple dibuat dari chunk immutable; re-upload menghasilkan document version baru |
| G-11 | Consent simpan paket arsip sesudah meeting | P0 | SELESAI LOKAL | pending→active→awaiting_decision; Save/Discard idempoten; stop/tab close/navigate/error terminal menuju keputusan; popup tidak memiliki pre-consent |
| G-12 | Revisi arsip tanpa kedaluwarsa otomatis | P0 | SELESAI LOKAL | Manifest schema v2+tamper verification, archive permanen, partial_failed eksplisit, staging-only purge, tombstone delete, dan runbook operator restore tersedia |

### H. Assistance AI

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| H-01 | Deteksi dan pemetaan pertanyaan peserta lain | P0 | PERLU VALIDASI | Final tab audio dipetakan otomatis tanpa filter penerima dengan stable source/hash; heuristic multilingual perlu evaluasi corpus nyata |
| H-02 | Aktivasi manual per pertanyaan | P0 | DI LUAR BATAS | Pengguna meminta otomatis; kontrol manual tidak dibutuhkan P0 |
| H-03 | Context builder meeting + dokumen khusus meeting | P0 | SELESAI LOKAL | Dokumen/chunk/retrieval terikat owner+occurrence; 10 file,20MiB/file,100MiB total |
| H-04 | Jawaban Sumopod via backend | P0 | PERLU VALIDASI | Client non-stream backend, key/model env, output JSON tervalidasi; model nyata belum diuji untuk pipeline jawaban |
| H-05 | Batas token, timeout, fallback | P0 | SELESAI LOKAL | Queue64, deadline15detik, max satu revisi; provider/retrieval failure menghasilkan status eksplisit dan tidak menghentikan capture |
| H-06 | Penyimpanan jawaban | P1 | SELESAI LOKAL | Questions/answers/citations masuk occurrence dan manifest arsip; RLS owner-only |
| H-07 | Retrieval dokumen dan kutipan sumber | P0 | PERLU VALIDASI | Lexical retrieval owner+occurrence; locator stabil PDF/DOCX/XLSX/CSV; OCR/container tersedia. Corpus representatif dan PDF scan nyata belum divalidasi |
| H-08 | Ringkasan meeting | P1 | DI LUAR BATAS | Tidak diminta untuk P0 |
| H-09 | Action items | P2 | DI LUAR BATAS | Tidak diminta untuk P0 |
| H-10 | Check Sumopod | P1 | PERLU VALIDASI | CLI tersedia; pemeriksaan akun/model nyata belum dijalankan sesi ini |
| H-11 | Jawaban Indonesia dan Inggris baku | P0 | PERLU VALIDASI | Kontrak strict JSON dan UI bilingual; grammar/model nyata belum dievaluasi |
| H-12 | Deteksi bahasa pertanyaan | P2 | BELUM DIMULAI | Pertanyaan diproses dari teks sumber; classifier bahasa terpisah belum diperlukan P0 |
| H-13 | Pemicu jawaban otomatis berbasis dokumen | P0 | PERLU VALIDASI | Pertanyaan final otomatis → retrieval → draft → validasi citation → maksimal satu revisi; no evidence/failure=`-` |

### I. Antarmuka pengguna

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| I-01 | Popup auth (login/register/logout) | P0 | SELESAI LOKAL | Alur auth berfungsi; status sesi tampil |
| I-02 | Popup status meeting | P1 | PERLU VALIDASI | Canonical URL, `meeting_id`, status sinkronisasi, retry |
| I-03 | Panel transcript live | P0 | PERLU VALIDASI | Transkrip streaming tampil di popup dari state `chrome.storage.session`: daftar final berurutan lalu partial terakhir, teks kosong diabaikan, dan status transkripsi punya pesan. Bukti yang ada: test lokal parser SSE, state transkrip, dan panel popup lulus (104/104 test extension pada 2026-10-22) dan jalur server terbukti live 2026-10-22 (SSE 3 partial + 1 final dari audio sampel). Bukti yang belum ada: panel ini belum pernah dibuka di Chrome nyata, jadi feature tetap PERLU VALIDASI sesuai aturan status roadmap |
| I-04 | Panel terjemahan | P0 | PERLU VALIDASI | Pasangan sumber↔Indonesia dan status gagal tampil; smoke Chromium terkelola lulus |
| I-05 | Panel bantuan AI | P1 | PERLU VALIDASI | Daftar bernomor, dua bahasa, citation locator, dan loading tampil; smoke Chromium terkelola lulus |
| I-06 | Halaman history meeting | P1 | PERLU VALIDASI | Popup memuat daftar archive owner dengan export/delete; native extension belum divalidasi |
| I-07 | Tampilan detail satu meeting | P1 | PERLU VALIDASI | Live transcript/translation/docs/Q&A dan archive action tersedia dalam popup |
| I-08 | Kontrol capture | P0 | SELESAI LOKAL | Tawaran Terhubung/Nanti terpisah dari klik Mulai; stop dan keputusan archive jelas |
| I-09 | Bahasa tujuan | P1 | SELESAI LOKAL | Target produk tunggal Indonesia; selector tidak diperlukan |
| I-10 | Empty/error/loading state | P1 | SELESAI LOKAL | Meeting, capture, translation, documents, questions, archives mempunyai state eksplisit |
| I-11 | Aksesibilitas | P1 | PERLU VALIDASI | Label/region/aria-live dan keyboard controls tersedia; aria smoke terkelola lulus, audit Chrome native belum |
| I-12 | Dark/light theme | P2 | SELESAI LOKAL | CSS mengikuti preferensi sistem |
| I-13 | Bahasa UI | P2 | SELESAI LOKAL | UI utama berbahasa Indonesia; jawaban Inggris ditandai `lang=en` |
| I-14 | Konfigurasi dan panel pengaturan | P1 | BELUM DIMULAI | Preferensi pengguna non-secret |

### J. Integrasi provider dan keandalan

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| J-01 | Client Deepgram | P0 | SELESAI LOKAL | Client streaming dengan deadline handshake, timeout write, dan teardown berbatas waktu; klog SDK tidak diaktifkan sehingga payload audio tidak pernah dicatat. Retry/backoff belum ada: reconnect otomatis di luar cakupan F4 |
| J-02 | Client Sumopod | P0 | SELESAI LOKAL | Chat completion non-stream, timeout, response limit, strict host check, tanpa prompt/response log |
| J-03 | Konfigurasi model dari environment | P0 | SELESAI LOKAL | Model wajib diisi; readiness menolak konfigurasi AI kosong |
| J-04 | Host provider dibatasi | P1 | SELESAI LOKAL | Check Sumopod membatasi host resmi; deployment tidak menerima redirect provider |
| J-05 | Redundansi provider | P2 | DI LUAR BATAS | Skala private team; tidak ada fallback yang berisiko mengirim data ke provider lain |
| J-06 | Handling rate limit/quota | P1 | PERLU VALIDASI | Provider refusal menjadi failed/`-` tanpa menghentikan capture; backoff/quota nyata belum diuji |
| J-07 | Retry transkripsi saat reconnect | P1 | DI LUAR BATAS | PRD tidak meminta reconnect otomatis; P0 berhenti eksplisit pada drop agar tidak menduplikasi/menghilangkan konteks tanpa kontrak resume provider |

### S. Keamanan, privasi, kepatuhan

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| S-01 | Secret hanya di backend | P0 | SELESAI LOKAL | Tidak ada secret di `extension/dist`, log, fixture, repo |
| S-02 | Larangan secret/service-role key pada aplikasi | P0 | SELESAI LOKAL | Config server utama menolak key tersebut; hanya one-shot storage-cleanup worker admin menerima secret terisolasi dan tidak diekspos ke extension/backend runtime |
| S-03 | Token tidak keluar dari `storage.session` | P0 | SELESAI LOKAL | `TRUSTED_CONTEXTS`; `local` untuk URL metadata |
| S-04 | RLS owner-only end-to-end | P0 | PERLU VALIDASI | Seluruh tabel/RPC termasuk archive verification dan cleanup worklist diuji dua owner pada PostgreSQL lokal; Supabase target/Storage policy belum divalidasi |
| S-05 | Ownership check pada semua endpoint meeting | P0 | SELESAI LOKAL | Token diverifikasi, client tidak menentukan user_id, repository memvalidasi owner/parent |
| S-06 | Budget permission extension | P0 | PERLU VALIDASI | Manifest tanpa mikrofon/audioCapture/global tabs; Chrome unpacked permission surface belum diperiksa manual |
| S-07 | Sanitasi URL dan pembuangan secret URL | P0 | SELESAI LOKAL | Passcode/JWT/fragment tidak disimpan |
| S-08 | Sanitasi prompt dan output | P0 | SELESAI LOKAL | Prompt menandai transcript/dokumen untrusted; JSON/citation index divalidasi dan output tanpa evidence ditolak database |
| S-09 | Rate limit API meeting | P1 | PERLU VALIDASI | Global inflight cap64 mencegah overload; rate per user/provider dan respons429 belum diimplementasikan |
| S-10 | Threat model end-to-end | P1 | SELESAI LOKAL | Plan keamanan dan runbook mencakup token, URL, audio, RLS, prompt injection, storage, backup, incident |
| S-11 | Kebijakan privasi dan consent | P0 | SELESAI LOKAL | Klik capture eksplisit, consent archive pascameeting, audio tidak disimpan, staging24jam, archive permanen dijelaskan |
| S-12 | Redaction data sensitif | P1 | TERBLOKIR KEPUTUSAN | Redaction isi tidak diminta; log metadata-only sudah diterapkan |

### L. Observabilitas dan operasi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| L-01 | Log terstruktur tanpa secret | P0 | SELESAI LOKAL | Request log hanya trace_id, method, path tanpa query, status, duration; body/token/prompt tidak dicatat |
| L-02 | Health dan readiness terpisah | P1 | SELESAI LOKAL | `/health` liveness; `/ready` validasi konfigurasi Supabase/Deepgram/Sumopod tanpa paid call |
| L-03 | Metrik biaya token dan durasi | P1 | BELUM DIMULAI | Token usage provider tidak tersedia pada kontrak client saat ini; jangan mengarang biaya |
| L-04 | Trace ID korelasi | P2 | PERLU VALIDASI | Backend menghasilkan `X-Trace-ID` dan log korelasi; extension/provider propagation belum ada |
| L-05 | Dokumentasi operator | P1 | SELESAI LOKAL | `docs/implementation/operator-runbook.md`: deploy, migration, isolated Storage cleanup worker, key rotation, incident, validation, operator-only restore, rollback |
| L-06 | Backup dan pemulihan database | P2 | PERLU VALIDASI | Runbook consistency-set DB+private objects dan no-resurrection tersedia; drill restore nyata belum dijalankan |

### M. Pengujian dan quality gate

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| M-01 | Test unit backend (`go test -race`) | P0 | SELESAI LOKAL | Semua paket termasuk assistance/documents/occurrence/audio lulus setelah implementasi |
| M-02 | `go vet` bersih | P0 | SELESAI LOKAL | Tidak ada temuan |
| M-03 | Test extension (`node --test`) | P0 | SELESAI LOKAL | 151/151 dan Vite build lulus; popup surface smoke Chromium terkelola |
| M-04 | Contract test auth | P0 | SELESAI LOKAL | 7/7 baseline lulus |
| M-05 | Test migrasi PostgreSQL sementara | P0 | SELESAI LOKAL | 0002–0011: reapply, owner isolation, occurrence, translation, documents, QA, archive, expiry dan concurrency lulus |
| M-06 | Fixture URL provider | P0 | SELESAI LOKAL | Fixture bersama frontend/backend |
| M-07 | Uji manual Chrome + provider nyata | P0 | PERLU VALIDASI | Popup surface terkelola terbukti; Chrome unpacked/tabCapture/AudioWorklet + meeting/provider nyata belum |
| M-08 | Uji live Supabase | P0 | PERLU VALIDASI | Baseline auth/meeting pernah live; migration0006–0011, Storage dan Cron target belum diterapkan |
| M-09 | Uji live provider berbayar | P1 | PERLU VALIDASI | Deepgram baseline2026-10-22 pernah lulus; Sumopod translation/QA dan matriks bahasa belum dijalankan |
| M-10 | Uji race & concurrency | P0 | SELESAI LOKAL | SQL lifecycle/archive/cleanup races dan writer/auth/extension races lulus; target3meeting/p95 masih gate nyata |
| M-11 | Uji offline/reconnect | P1 | DI LUAR BATAS | PRD/P0 memilih stop eksplisit pada disconnect; kontrak resume provider harus dirancang sebelum retry diuji |
| M-12 | Uji data besar | P2 | PERLU VALIDASI | Parser/chunk/file/queue limits ada; meeting4jam dan100MiB dokumen nyata belum dijalankan |
| M-13 | Uji provider outage | P1 | SELESAI LOKAL | Unit error/timeout/refusal memisahkan translation/QA failure dari capture |
| M-14 | Uji upgrade migrasi | P1 | PERLU VALIDASI | Reapply seluruh migration lulus lokal; upgrade Supabase target belum |
| M-15 | Uji live API capture | P0 | PERLU VALIDASI | Baseline run pernah live; occurrence contract baru lulus lokal dan menunggu target nyata |

### N. Rilis dan distribusi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| N-01 | Packaging extension siap distribution | P1 | SELESAI LOKAL | Vite build bersih; container production Poppler/Tesseract berhasil dibangun |
| N-02 | Dokumentasi pengguna | P1 | SELESAI LOKAL | README menjelaskan connect, tab-only, upload, QA, consent, archive, batas provider |
| N-03 | Environment deployment | P1 | PERLU VALIDASI | Compose image lulus build dan readiness ada; secret/TLS/Supabase target belum diaktifkan |
| N-04 | Checklist rilis | P1 | PERLU VALIDASI | Gate nyata dirinci runbook; belum rilis sebelum Chrome/provider/cloud/latency/restore lulus |
| N-05 | Rencana rollback | P2 | SELESAI LOKAL | Rollback image forward-compatible; staging cleanup tetap aktif; tidak resurrect data |
| N-06 | Halaman Store listing | P2 | BELUM DIMULAI | Distribusi private managed/restricted disarankan; listing belum dibuat |

---

## 6. Permukaan data yang direncanakan

| Tabel | Isi | Milik | Status |
| --- | --- | --- | --- |
| `public.profiles` | Identitas dan `display_name` | Pengguna | Terapan (0001) |
| `public.meeting_sessions` | Identitas link meeting per pengguna | Pengguna | Terapan (0002) |
| `public.meeting_runs` | Metadata satu sesi capture (tanpa audio) | Pengguna | Terapan (0003) |
| `public.transcript_segments` | Segmen final staging/archive per occurrence | Pengguna | Migration0004+0006; target cloud perlu validasi |
| `public.translations` | Terjemahan Indonesia per source segment/revision | Pengguna | Migration0007, SELESAI LOKAL |
| `public.documents` / `document_chunks` | Private object metadata, version, locator, retrieval text | Pengguna | Migration0008, PERLU VALIDASI Storage target |
| `public.questions` / `answers` | Pertanyaan stabil, bilingual answer, citation | Pengguna | Migration0009, SELESAI LOKAL |
| `public.assistance_segment_status` | Bukti setiap segmen final sudah diproses assistance sebelum commit | Pengguna | Migration0009, SELESAI LOKAL |
| `public.archives` | Manifest permanen schema v2 termasuk status pemrosesan dan delete tombstone | Pengguna | Migration0010, SELESAI LOKAL |
| `public.storage_cleanup_jobs` | Worklist durable penghapusan object private via Storage API | Pengguna/operator | Migration0010, SELESAI LOKAL; target worker perlu validasi |

Setiap tabel baru wajib punya: `id` UUID, `user_id` FK, timestamp, unique constraint yang sesuai, grants minimum, dan RLS owner-only.

## 7. Permukaan API yang direncanakan

| Method | Path | Auth | Status |
| --- | --- | --- | --- |
| GET | `/health` | — | Selesai |
| POST | `/api/auth/register` | — | Perlu validasi |
| POST | `/api/auth/login` | — | Perlu validasi |
| POST | `/api/auth/refresh` | — | Perlu validasi |
| POST | `/api/auth/logout` | Bearer | Perlu validasi |
| GET | `/api/auth/me` | Bearer | Perlu validasi |
| POST | `/api/meetings/resolve` | Bearer | Perlu validasi |
| GET | `/api/meetings` | Bearer | Perlu validasi |
| GET | `/api/meetings/{id}` | Bearer | Perlu validasi |
| POST | `/api/audio/runs` | Bearer | Occurrence-bound: meeting_id+occurrence_id+sample_rate; pre-capture save option ditolak |
| POST | `/api/audio/runs/{id}/chunks` | Bearer | Byte/chunk atomik; 2MiB chunk,1GiB run,4jam |
| POST | `/api/audio/runs/{id}/stop` | Bearer | Stop idempoten |
| GET | `/api/audio/runs/{id}/transcript` | Bearer | SSE partial/final/end/error |
| GET | `/api/audio/runs/{id}/translations` | Bearer | Translation linked ke source run |
| POST/GET | `/api/occurrences/*` | Bearer | Connect, activate, finish, archive, discard, status |
| POST/GET | `/api/documents` | Bearer | Multipart upload/extract dan list occurrence |
| GET | `/api/questions?occurrence_id=…` | Bearer | Numbered QA + bilingual answer + citations |
| GET/DELETE | `/api/archives*` | Bearer | History keyset, detail, ZIP lengkap/integrity checked, explicit soft-delete + durable object cleanup |
| GET | `/ready` | — | Validasi konfigurasi tanpa paid request |

## 8. Urutan implementasi yang dijaga

```text
Validasi F1 Auth/Supabase nyata  ✓ live 2026-10-21 (sisa signup + Chrome)
  → Validasi F2 Meeting/Chrome nyata ✓ live 2026-10-21 (sisa Chrome)
  → DEC-01 + DEC-05 → F3 Audio Capture ✓ selesai 2026-10-21
  → F4 Streaming Transcription ✓ selesai lokal 2026-10-22; jalur server terbukti live 2026-10-22 (sisa validasi Chrome)
  → F6 Storage & timeline: implementasi dan verifikasi lokal selesai; migration Supabase target + Chrome unpacked/native masih PERLU VALIDASI
  → DEC-07 + DEC-08 (bahasa & provider) → F5 Translation
  → DEC-11 + DEC-13 → F8 Dokumen/retrieval/citation (kebutuhan inti, bukan opsional)
  → DEC-12 + kontrak F8 → F7 Assistant berbasis dokumen
  → F9 History & ringkasan
  → F10 Hardening & rilis
```

Aturan: agent tidak boleh melompati dependency dengan mock, stub, no-op, atau fallback palsu. Spike eksplorasi hanya boleh jika dinyatakan throwaway dan disetujui pengguna.

## 9. Keputusan terbuka

Setiap keputusan mengunci fitur tertentu. Gunakan brainstorming/spec, lalu catat hasilnya di feature terkait dan hapus entry ini.

| ID | Keputusan | Mengunci | Batas default sementara |
| --- | --- | --- | --- |
| DEC-02 | Deteksi joined-state via DOM | C-21 | Deteksi berbasis URL saja |
| DEC-06 | Speaker diarization | E-10 | Tidak ada label speaker |
| DEC-07 | Dukungan bahasa sumber dan code-switching | F-01, F-02 | Tujuan Indonesia ditetapkan2026-10-24; cakupan bahasa sumber, deteksi dan batas provider masih perlu desain |
| DEC-09 | Glosarium istilah | F-08 | Tanpa glosarium |
| DEC-17 | Redundansi provider | J-05 | Satu provider per jenis |
| DEC-18 | Consent dan redaksi data sensitif | S-11, S-12 | Tidak ada redaksi otomatis |

## 10. Protokol pembaruan oleh agent

1. Baca dokumen ini dan dokumen status fitur terkait sebelum perubahan.
2. Tentukan ID fitur yang disentuh; jangan memperluas scope diam-diam.
3. Perbarui tabel katalog: status, prioritas, dependensi, atau acceptance yang berubah.
4. Status naik hanya dengan bukti sesuai Bagian 2.1 dan acceptance fitur terkait.
5. Keputusan baru: pindahkan isi keputusan dari Bagian 9 ke feature terkait, lalu hapus barisnya di Bagian 9.
6. Tambahkan fitur baru hanya dari instruksi pengguna atau spec yang disetujui; tandai inferensi eksplisit.
7. Perbarui `Terakhir diperbarui` dan tabel changelog di Bagian 12.
8. Catat verifikasi ringkas: perintah yang dijalankan, hasil, dan batasnya. Jangan salin log panjang.
9. Ringkas tetap di sini; detail tetap di dokumen khusus dan ditautkan dari feature terkait.
10. Sinkronkan ke Obsidian project memory. Bila MCP tidak tersedia, perbarui `docs/obsidian-pending/pending.md` tanpa menggandakan entry.

## 11. Bukti baseline terakhir

Terverifikasi pada 2026-10-21 di workspace ini:

- Backend: `go test -race -count=1 ./...`, `go vet ./...`, build binary, smoke `GET /health` → lulus.
- Extension: 41/41 test dan build Vite 7.3.7 → lulus (2026-10-21); 104/104 test pada 2026-10-22 setelah penambahan transkripsi → lulus.
- Contract auth: 7/7 → lulus.
- `docker compose config --quiet` → lulus.
- Migrasi meeting pada PostgreSQL sementara (idempotensi, identity stabil, grants, RLS) → lulus.
- Live pada project Supabase nyata: `0001` dan `0002` terpasang; login, `/me`, refresh (rotasi), logout, token salah, password salah → lulus; resolve/list/detail meeting, idempotensi, kanikalisasi query/fragment, alias Zoom, normalisasi Jitsi, race paralel, penolakan URL, 404 ID asing, isolasi dua akun, dan RLS langsung → lulus; re-apply `0002` → exit 0 tanpa merusak data.
- Backend container nyata berjalan di `http://localhost:8989` dengan `GET /health` → `{"service":"livetranslate-backend","status":"ok"}`.
- F3 Audio Capture: migrasi `0003` terpasang live; API run/chunk/stop terbukti live (akuntansi atomik, 409 setelah stop, 401 tanpa token, 404 untuk meeting akun lain); extension 49/49 test dan build menghasilkan `dist/offscreen.html`; audio tidak disimpan di mana pun.
- F4 Transkripsi Streaming (2026-10-22): `go test ./...` dan `go vet ./...` lulus (paket `internal/transcribe` dan `internal/audio` mencakup provider, broker, backlog, route run/chunk/stop/SSE); `npm test` 104/104 lulus; `npm run build` lulus; `node --test http/auth.test.mjs` 7/7 lulus; `docker compose config --quiet` lulus; `sh migration/tests/check-meetings.sh` lulus. Model `nova-2` dan bahasa `multi` diuji terhadap akun nyata (`2-general` dan `2-meeting` ditolak; `multi`, `en-US`, `id` diterima). Audio dan transkrip tidak ditulis ke database, log, atau file.
- F4 live (2026-10-22, container nyata + provider nyata): `docker compose up -d --build backend` → container jalan di `http://127.0.0.1:8989`; `GET /health` → `{"service":"livetranslate-backend","status":"ok"}`; `docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav` → `deepgram: LULUS`; login akun uji nyata → 200 dengan sesi; resolve meeting → 200 dengan `meeting_id`; `POST /api/audio/runs` (`sample_rate` 8000) → 201 run `active`; dua unggah chunk PCM16 (32.000 + 42.880 byte) → 200 dengan akuntansi 0 → 32.000 → 74.880 byte dan 0 → 1 → 2 chunk; `GET /api/audio/runs/{id}/transcript` → 3 event `segment` partial + 1 final + event `end`; `POST .../stop` → 200 `stopped` dengan `ended_at`, chunk setelah stop 409, stop kedua 200, stream run yang sudah stop hanya `end`, akun kedua 404; baris `meeting_runs` di Supabase nyata hanya berisi status dan akuntansi; log container tidak memuat secret, audio, atau teks transkrip.
- Belum diuji end-to-end: signup sukses pada project nyata (rate limit email project), capture audio native di Chrome, streaming Deepgram dari browser, Sumopod nyata. Baseline2026-10-22 tidak memiliki binary Chrome; sesi F6 memakai Chromium terkelola untuk UI terkontrol, bukan capture native. Jalur server Deepgram sudah terbukti live pada baseline.

### 11.1 Catatan operasional hasil validasi live

- Docker daemon dapat diakses; `docker compose up -d --build --remove-orphans backend` membangun image dan menjalankan backend pada port host 8989.
- Migrasi dapat diterapkan tanpa SQL Editor memakai image `postgres:18-alpine` dan `psql` dengan string koneksi dari `.env.before-supabase-api` (backup yang diabaikan Git). Skrip ini tidak pernah membaca `.env` aktif dan tidak mencetak kredensial.
- GoTrue mensyaratkan kolom string pada `auth.users` bernilai string kosong. Akun yang dibuat di luar GoTrue dengan `NULL` membuat password grant gagal 500 `Database error querying schema`; kolom yang harus `''`: `confirmation_token`, `email_change`, `email_change_token_current`, `email_change_token_new`, `recovery_token`, `reauthentication_token`, `phone_change`, `phone_change_token`.
- `raw_user_meta_data` akun non-GoTrue harus memuat `sub`, `email`, `email_verified`, `phone_verified`, dan `display_name`, mengikuti bentuk yang ditulis GoTrue.
- Akun uji yang ada di project: `livetranslate.e2e@livetranslate.invalid` dan `livetranslate.e2e2@livetranslate.invalid` (domain tidak dapat delivering, jadi tidak ada email keluar). Hapus lewat dashboard atau `DELETE FROM auth.users WHERE email LIKE 'livetranslate.e2e%'` bila tidak diperlukan lagi; baris `public.profiles` dan `public.meeting_sessions` ikut terhapus oleh FK cascade.
- Project membatasi pengiriman email konfirmasi; signup yang berulang akan menerima 429 `over_email_send_rate_limit`. Semua request AI berbayar tetap ditahan.

Dokumen status rinci: `docs/implementation/auth-status.md`, `docs/implementation/meeting-status.md`, `docs/obsidian-pending/2026-10-19-implementation-analysis.md`.

### 11.2 Bukti F6 — 2026-10-24 lokal / 2026-10-23 UTC

- Go `go test -race -count=1 -timeout=120s ./...`, vet dan build lulus; extension147/147 + build lulus; auth contract7/7 dan Compose config lulus.
- Migration0004/harness PostgreSQL18.6 membuktikan RLS/direct mutation, final immutable/idempoten, parent-lock deletion/expiry races, reapply dan physical purge.
- PostgreSQL18.6 + PostgREST16.5 + pg_cron1.6.8 lokal: migration0005 reapply tetap satu job aktif; actual scheduler menghapus teks expired dan mempertahankan metadata serta final belum expired.
- Smoke production Go handler dengan provider/identitas terkontrol: opt-out kosong;206final dalam5halaman, sequence gaps dipertahankan, partial tidak tersimpan;401/404 akun lain; delete saat capture aktif tidak mematikan capture atau memulihkan teks, repeat delete sukses.
- Popup React/worker aktual di Chromium150 dengan bridge Chrome terkontrol: consent defaultfalse, opt-in start/stop dan next-run reset, timeline50→100, file JSON31.141byte/206final/through411/complete=true, delete ke0baris dan run berikutnya opt-out. Screenshot/unduhan diperiksa; tidak ada page exception.
- Tidak ada migration cloud, provider berbayar atau capture native pada sesi F6. Fase tetap PERLU VALIDASI, bukan produksi; [status dan aktivasi F6](transcript-storage-status.md). Catatan F3/F4 di atas adalah bukti historis, bukan hasil ulang sesi ini.


### 11.3 Bukti implementasi paket P0 — 2026-10-24

- Migration harness `sh migration/tests/check-meetings.sh` lulus untuk `0002`–`0011`, termasuk reapply, dua-owner RLS, occurrence lifecycle, satu active run per owner, translation/assistance overflow reconciliation, document locator/limits, citation owner+occurrence, archive completeness/integrity, larangan delete per-run pada archive committed, staging expiry, dan concurrency.
- Backend `go test -race -count=1 -timeout=120s ./...`, `go vet ./...`, dan build kedua binary lulus setelah seluruh review fix.
- Extension `npm test && npm run build` lulus 151/151. Popup aktual dijalankan di Chromium terkelola dengan bridge terkontrol; pagination dan detail archive tampil, tombol delete per-run archive tidak ada, dan page/console tidak memiliki error.
- `docker compose config --quiet` dan production image build lulus. Container aktual merespons `/health` dengan status process dan `/ready` dengan `{"ready":true}`; image memasang Poppler serta Tesseract English+Indonesian.
- Review akhir kedua tidak menemukan temuan penting setelah nullable permanent-run listing, `quality_flags` array, stop/logout acknowledgement, nonblocking derived queues, explicit parser/quota states, multi-question bounds, archive detail/pagination, dan archive deletion boundary diperbaiki.
- Belum dijalankan: migration/Storage/Cron pada Supabase target, Chrome unpacked native tabCapture, Sumopod nyata, language matrix/code-switching, p95 tiga meeting, 100MiB/4jam soak, archive hash pada Storage nyata, dan restore drill. Semua tetap `PERLU VALIDASI`; bukan siap produksi.

## 12. Changelog roadmap

| Tanggal | Perubahan | Sumber |
| --- | --- | --- |
| 2026-10-21 | Roadmap kanonis dibuat: arah, status, fase, batas, urutan, keputusan terbuka, protokol update | Permintaan pengguna "catat fitur yang harus diimplementasikan" |
| 2026-10-21 | Diperluas menjadi katalog fitur menyeluruh (A–N + S) dengan ID, prioritas, status, dependensi, acceptance; tabel/endpoint direncanakan; tabel changelog | Permintaan pengguna "buat catatan fitur menyeluruh, se efektif mungkin" |
| 2026-10-21 | Perbaikan ID: domain keamanan dipindah dari `K-nn` ke `S-nn`; ID keputusan menjadi `DEC-nn` untuk menghapus ambiguitas rujukan | Audit konsistensi dokumen |
| 2026-10-21 | Validasi live F1/F2: migrasi 0001+0002 diterapkan ke project Supabase nyata, auth dan meeting diuji end-to-end, isolasi dua akun dan RLS terbukti; 13 fitur naik status, catatan operasional ditambahkan | Sesi validasi live 2026-10-21 |
| 2026-10-21 | F3 Audio Capture selesai: 4 keputusan pengguna dikunci (tab audio saja, tombol Mulai, tanpa penyimpanan audio, `meeting_run_id` per capture); migrasi 0003 + endpoint run/chunk/stop + dokumen offscreen; DEC-01, DEC-03, DEC-04 ditutup; 12 fitur naik status | Sesi implementasi F3 2026-10-21 |
| 2026-10-21 | Spec F4 transkripsi live ditulis setelah spike Deepgram: model akun `nova-2` (bukan `2-general`), format wire PCM16 (container webm tidak menghasilkan segmen final), auth streaming lewat header; D-07 turun ke PERLU VALIDASI | Sesi spike dan desain F4 2026-10-21 |
| 2026-10-22 | F4 Transkripsi Streaming selesai secara lokal: capture PCM16 mono dari `AudioWorklet` dengan frame 100 ms (D-07 naik ke SELESAI LOKAL), backend meneruskan byte ke Deepgram streaming, backlog segmen per run, endpoint SSE `/api/audio/runs/{id}/transcript`, state transkrip di popup. E-02, E-04, E-05, E-06, E-07, I-03, J-01 naik ke SELESAI LOKAL; E-01, E-09 tetap PERLU VALIDASI (belum ada validasi browser/live); E-03 reconnect tetap di luar cakupan F4. Konfigurasi `DEEPGRAM_MODEL=nova-2` dan `DEEPGRAM_LANGUAGE=multi`. Bukti: `go test ./...`, `go vet ./...`, `npm test` 104/104, `npm run build`, contract auth 7/7, `docker compose config --quiet`, `sh migration/tests/check-meetings.sh` | Sesi implementasi F4 2026-10-21 s.d. 2026-10-22 |
| 2026-10-22 | Verifikasi live F4: container nyata dibangun dan dijalankan, `-check deepgram` lulus dengan key nyata, alur login → resolve → run → chunk → SSE → stop terbukti live (3 partial + 1 final dari audio sampel SDK), isolasi akun 404, tidak ada audio/transkrip di database, log, atau file. I-08 (kontrol capture) naik dari BELUM DIMULAI ke SELESAI LOKAL karena tombol popup dan worker sudah ada sejak F3; E-01, E-08, A-11, M-09 naik ke SELESAI LOKAL; M-07 turun ke PERLU VALIDASI karena hanya sisi Chrome yang belum dijalankan. F4 tetap PERLU VALIDASI (validasi Chrome) | Sesi verifikasi akhir F4 2026-10-22 |
| 2026-10-22 | Perbaikan review akhir F4: segmen final tidak hilang lagi saat run dihentikan (server stop mendahului penutupan dokumen, jendela tunggu final mengikuti anggaran tutup 10 s), penolakan terminal backend menghentikan capture dan menutup run, provider yang mati dikeluarkan dari broker dan run ditandai `failed`, kegagalan provider tidak lagi dilaporkan sebagai `end`, dan klaim dokumen diselaraskan dengan kode. I-03 turun dari SELESAI LOKAL ke PERLU VALIDASI karena panel transcript belum pernah dibuka di Chrome nyata | Sesi perbaikan review akhir F4 2026-10-22 |
| 2026-10-23 | Konteks sesi diselaraskan dengan roadmap terbaru: F3 sudah tersedia dan F4 masih perlu validasi Chrome; tidak dibuat ulang. Pengguna menetapkan F6: simpan transkrip final opt-in per capture (default mati), retensi 30 hari, timeline/pemilihan run melalui popup, hapus transkrip per run dan ekspor JSON. F6, G-02, G-05…G-08 naik ke SIAP DIRANCANG, bukan selesai; DEC-05/DEC-10 tetap terbuka hanya untuk artefak selain transkrip. Draft desain telah menjalani builder check dan review privacy/transaction; kontrak delete langsung Data API, provenance dan expiry setelah lock diperjelas. Persetujuan penuh belum diberikan; belum ada perubahan kode atau verifikasi runtime F6 | Keputusan pengguna; `docs/superpowers/specs/2026-10-23-transcript-storage-design.md`; `docs/obsidian-pending/2026-10-23-roadmap-resume.md` |
| 2026-10-23 | Pengguna menyetujui desain F6 lengkap dan plan, lalu memilih implementasi paralel pada workspace yang ada dengan perubahan F4 dipertahankan. Implementasi dibagi migration/RLS/retensi, backend writer/API, dan popup/ekspor. Regresi awal endpoint arsip gagal sebagaimana diharapkan (404, seharusnya 401 untuk anonim) sebelum perubahan produk. Status belum dinaikkan karena verifikasi implementasi belum dijalankan | Persetujuan pengguna; `docs/superpowers/plans/2026-10-23-transcript-storage.md` |
| 2026-10-24 | F6 diimplementasikan dan diverifikasi lokal: migration0004/0005, writer final bounded/ownership/drain, snapshot API, terminal delete, default-off consent, timeline dan ekspor lengkap. Go race/vet/build, extension147/147+build, auth7/7, Compose, SQL/RLS/concurrency, actual pg_cron purge, real Go/PostgREST/PG smoke206final/5halaman dan popup/file JSON lulus. G-02/G-05…G-08 SELESAI LOKAL; F6 dan S-04 PERLU VALIDASI karena target Supabase/native Chrome belum diuji. Keputusan translation/AI tetap terbuka | `docs/implementation/transcript-storage-status.md`; desain/plan yang disetujui |
| 2026-10-24 | Brainstorm kebutuhan produk: tawaran koneksi otomatis, terjemahan ke Indonesia, catatan untuk deteksi pertanyaan, jawaban otomatis berbasis dokumen dengan kutipan. H-07 menjadi P0 produk, F-03 arah ditetapkan/belum diimplementasikan; kebutuhan teknis bahasa sumber, capture keseluruhan, consent, dokumen dan iterasi masih terbuka. Tidak ada implementasi atau uji runtime baru | Permintaan pengguna; `docs/obsidian-pending/2026-10-24-planning-realignment-brainstorm.md` |
| 2026-10-24 | Pengguna menegaskan mikrofon sendiri tidak perlu ditranskripsikan/diterjemahkan; hanya audio peserta lain. Batas tab-only D-01/D-10 dipertahankan, tanpa perubahan kode/status implementasi. Consent penyimpanan dan cakupan dokumen masih dibahas | Klarifikasi pengguna dalam brainstorming |
| 2026-10-24 | Pengguna menetapkan catatan terjemahan terlihat live dan tawaran simpan setelah meeting selesai. Tambah G-11 P0 revisi consent/lifecycle F6; baseline implementasi lama tidak memenuhi requirement baru. Detail staging, retensi, kelengkapan, akhir meeting dan crash menunggu desain; tanpa perubahan kode atau uji runtime | Keputusan pengguna dalam brainstorming |
| 2026-10-24 | Pengguna menyebut PDF, DOCX dan spreadsheets sebagai keluarga format sumber jawaban. Varian spreadsheet, OCR, cakupan dokumen dan batas ingestion masih perlu desain; usulan parser modular/locator kutipan dicatat terpisah dari keputusan pengguna. Tanpa implementasi | Brainstorm dokumen pengguna |
| 2026-10-24 | Pengguna memilih dokumen diunggah khusus per meeting, menggantikan usulan perpustakaan akun. Isolasi H-03 meliputi sumber dan turunannya; retensi/penghapusan sesudah meeting belum diputuskan. Tanpa perubahan implementasi | Klarifikasi pengguna tentang cakupan dokumen |
| 2026-10-24 | Pengguna menyetujui dokumen sumber dan jawaban AI ikut disimpan bersama catatan/kutipan saat memilih simpan hasil meeting. DEC-14 penyimpanan jawaban ditutup; kontrak paket, retensi dan penghapusan masih perlu desain G-11/DEC-05/DEC-10. Tidak mengizinkan penggunaan dokumen lintas meeting atau penyimpanan audio | Persetujuan pengguna dalam brainstorming |
| 2026-10-24 | Pengguna menolak retensi30hari dan meminta arsip disimpan selamanya: tanpa expiry/purge berbasis umur. Tambah G-12 P0 revisi kebijakan arsip; bukti G-06 lama ditandai historis. Staging belum disetujui bukan arsip permanen. Hanya planning, tidak menghapus data atau mengubah scheduler/kode | Keputusan pengguna dalam brainstorming |
| 2026-10-24 | Pengguna menetapkan abstention: jika dokumen tidak ada atau tidak mendukung jawaban, nyatakan tidak tersedia dari dokumen. Tidak boleh fallback ke pengetahuan umum atau mengarang kutipan. Kegagalan ekstraksi/retrieval harus dibedakan dari hasil pencarian tanpa bukti | Keputusan pengguna dalam brainstorming |
| 2026-10-24 | Pengguna memilih semua pertanyaan peserta lain, termasuk untuk peserta selain dirinya, tanpa filter nama/penerima. Setiap pertanyaan dipetakan bernomor ke jawaban dan bagian dokumen; jawaban kosong/`-` bila bukti tidak tersedia. Pilihan penulis memakai `-` konsisten; identitas item dan deduplikasi perlu kontrak. Tanpa implementasi | Klarifikasi pemetaan pertanyaan oleh pengguna |
| 2026-10-24 | Pengguna menetapkan jawaban AI dalam bahasa Indonesia dan Inggris yang baku/grammatikal. DEC-16 ditutup; H-11 menjadi P0 kebutuhan inti, BELUM DIMULAI. Terjemahan percakapan tetap ke Indonesia, bukan otomatis subtitle dwibahasa | Keputusan pengguna dalam brainstorming |
| 2026-10-24 | Pengguna menetapkan tim sangat kecil, mungkin di bawah3orang. Rekomendasi arsitektur tetap satu backend Go modular; tidak ada alasan skala untuk microservice per fitur. Akses aplikasi terbatas dan kebijakan akses antaranggota perlu dirancang; owner-only tetap baseline. Tidak ada perubahan kode atau klaim load test | Keputusan pengguna tentang sasaran rilis |
| 2026-10-24 | Pengguna menegaskan akses hanya milik sendiri. Draft PRD/desain konsolidasi ditulis dengan rekomendasi terpisah (staging, batas sumber daya, OCR, latency) untuk persetujuan. Pemeriksaan dokumen oleh penulis, tanpa review independen atau uji runtime; belum plan eksekusi lengkap | `docs/superpowers/specs/2026-10-24-product-realignment-design.md` |
| 2026-10-24 | Paket planning eksekusi lengkap disusun: master plan + 5 detail (arsitektur/data, lifecycle/arsip, realtime/ekstensi, dokumen/AI, keamanan/operasi). Tujuh plan/spec lama ditetapkan historis. Pengguna kemudian mengotorisasi implementasi paket P0; baseline awal lulus `go test -race -count=1 ./...` dan extension `npm test` 147/147 sebelum perubahan baru. Status fitur belum dinaikkan. | Instruksi pengguna; `docs/superpowers/plans/2026-10-24-execution-master.md` dan `docs/superpowers/plans/2026-10-24-details/` |
| 2026-10-24 | Paket P0 diimplementasikan lokal: occurrence+postmeeting consent, translation Indonesia, PDF/DOCX/XLSX/CSV+OCR, lexical retrieval, numbered bilingual evidence-only Q/A, permanent complete archive/export/delete, readiness/log/trace/runbook. SQL harness, backend race/vet, extension148/build, popup Chromium smoke, Compose config dan production image build lulus. Prioritas berpindah ke validasi Chrome/Supabase/provider/language/latency/restore nyata; status terkait tetap PERLU VALIDASI | Implementasi sesi ini; `.superpowers/sdd/2026-10-24-execution-master/progress.md`; `docs/implementation/operator-runbook.md` |
| 2026-10-24 | Hardening review P0: redirect dan host Sumopod dikunci, readiness memvalidasi endpoint provider, citation harus cocok dengan document chunk occurrence, setiap segmen final memiliki status assistance, commit arsip menolak derived artifacts yang belum lengkap, dan export memverifikasi byte count+SHA-256 file sumber. Focused Go test dan SQL/RLS harness lulus; gate layanan/browser nyata tetap terbuka. | Review implementasi dan regression test sesi ini |
| 2026-10-24 | Review penutup P0 memperbaiki 14 risiko lintas lifecycle/data/UI: permanent run dengan expiry `NULL` kembali tampil, archive document menerima `quality_flags` array PostgREST, stop/logout menunggu acknowledgement, queue derived tidak memblok transcript, satu active run/owner ditegakkan, multipart/PDF bounds eksplisit, status parse/quota tersedia, beberapa pertanyaan dipetakan dengan overflow eksplisit, history keyset/detail tersedia, dan delete per-run tidak dapat merusak archive committed. Full Go race/vet/build, extension151/build, auth7, SQL/RLS/concurrency, Compose/image, container health/readiness, popup Chromium, secret scan, dan re-review lulus lokal. P0 selesai lokal; prioritas berikutnya hanya gate layanan/browser nyata yang tetap PERLU VALIDASI. | Dua putaran code review dan verification-before-completion sesi ini |
