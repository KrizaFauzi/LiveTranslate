# LiveTranslate — Feature Roadmap & Katalog Fitur

> Dokumen kanonis arah produk, katalog fitur, status implementasi, dependensi, dan kriteria selesai.
>
> **Aturan agent:** baca dokumen ini sebelum merancang atau mengimplementasikan fitur apa pun. Setelah pekerjaan mengubah status, scope, dependensi, keputusan, atau hasil verifikasi, perbarui dokumen ini pada **sesi yang sama**. Jangan menandai fitur selesai berdasarkan kode saja;lampirkan bukti sesuai definisi status dan kriteria selesai.
>
> **Satu sumber kebenaran:** roadmap ini adalah daftar fitur dan statusnya. Detail desain, plan, dan hasil uji tetap berada pada dokumen khusus (`docs/implementation/`, `docs/superpowers/`, `docs/obsidian-pending/`) dan hanya diringkas di sini.

Terakhir diperbarui: 2026-10-22 (termasuk verifikasi live F4: container nyata dan provider nyata)  
Sumber arah: implementasi repository, `README.md`, status autentikasi, status Tab Meeting Capture, rencana inisialisasi, dan keputusan pengguna.

---

## 1. Arah aplikasi

LiveTranslate adalah **Chrome extension pendamping meeting berbasis browser**. Pengguna login, extension mengenali konteks meeting, menangkap audio yang diizinkan, mengubah percakapan menjadi transkrip dan terjemahan, lalu memberi bantuan berbasis konteks meeting tanpa mencampur data antar pengguna atau antar meeting.

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
| F5 | Terjemahan | TERBLOKIR KEPUTUSAN | Terjemahan real-time per segmen |
| F6 | Penyimpanan & timeline | TERBLOKIR KEPUTUSAN | Artefak meeting per run |
| F7 | Assistance AI | TERBLOKIR KEPUTUSAN | Deteksi pertanyaan + jawaban |
| F8 | Retrieval/RAG | TERBLOKIR KEPUTUSAN | Konteks relevan per meeting |
| F9 | History & ringkasan | TERBLOKIR KEPUTUSAN | Daftar meeting, ringkasan, ekspor |
| F10 | Hardening & rilis | BELUM DIMULAI | Threat review, uji outage, packaging |

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
| C-20 | `meeting_run_id` per occurrence | P1 | SELESAI LOKAL | `meeting_id` tetap identitas link; tiap capture membuat `meeting_run_id` baru pada tabel `meeting_runs` |
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
| D-04 | Lifecycle start/stop deterministik | P0 | SELESAI LOKAL | Capture berhenti saat tombol Berhenti ditekan, tab meeting ditutup, pengguna logout, atau error. Menutup popup tidak menghentikan capture |
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
| F-01 | Terjemahan segmen live | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-07 (bahasa) dan DEC-08 (provider/model) |
| F-02 | Bahasa sumber detection/selection | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-07 |
| F-03 | Bahasa tujuan tunggal/multi | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-07 |
| F-04 | Referensi segmen sumber pada terjemahan | P0 | TERBLOKIR KEPUTUSAN | Terjemahan tertaut ke segmen asalnya |
| F-05 | Perilaku koreksi sumber | P1 | TERBLOKIR KEPUTUSAN | Koreksi tidak menampilkan duplikasi permanen |
| F-06 | Latensi terjemahan | P1 | TERBLOKIR KEPUTUSAN | Target latensi ditetapkan dan diukur |
| F-07 | Batas biaya terjemahan | P1 | TERBLOKIR KEPUTUSAN | Batas token/permintaan dan degradasi saat kuota habis |
| F-08 | Glosarium istilah | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-09 |

### G. Penyimpanan dan timeline

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| G-01 | Skema `meeting_runs` | P0 | SELESAI LOKAL | `meeting_runs` terpasang live: id, user_id, meeting_id, status, bytes/chunks, started_at, ended_at; RLS owner-only; fungsi atomik untuk akuntansi |
| G-02 | Skema `transcript_segments` | P0 | TERBLOKIR KEPUTUSAN | Kepemilikan per baris, urutan stabil, ingest idempoten |
| G-03 | Skema `translations` | P0 | TERBLOKIR KEPUTUSAN | Terikat segmen dan bahasa tujuan |
| G-04 | RLS seluruh tabel meeting | P0 | PERLU VALIDASI | `meeting_sessions` dan `meeting_runs` terbukti live tanpa jalur baca lintas akun; tabel segmen dan terjemahan belum ada |
| G-05 | Endpoint append/list segmen | P0 | TERBLOKIR KEPUTUSAN | Append idempoten, list hanya milik pemilik |
| G-06 | Retensi data | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-05 |
| G-07 | Hapus/export pengguna | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-10 |
| G-08 | Timeline meeting | P1 | TERBLOKIR KEPUTUSAN | Urutan kronologis per run |
| G-09 | Embedding untuk retrieval | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-11 |
| G-10 | Indeks dan re-index | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-11 |

### H. Assistance AI

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| H-01 | Deteksi pertanyaan dari konteks | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-12 (aktivasi) dan DEC-13 (konteks) |
| H-02 | Aktivasi manual per pertanyaan | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-12 |
| H-03 | Context builder terisolasi per run | P0 | TERBLOKIR KEPUTUSAN | Hanya konteks run milik pengguna; tidak ada data meeting lain |
| H-04 | Jawaban Sumopod via backend | P0 | TERBLOKIR KEPUTUSAN | Model dari `SUMOPOD_MODEL` nyata; key hanya di backend |
| H-05 | Batas token, timeout, fallback | P0 | TERBLOKIR KEPUTUSAN | Kegagalan LLM tidak menghentikan capture/transkripsi |
| H-06 | Penyimpanan jawaban | P1 | TERBLOKIR KEPUTUSAN | Keputusan DEC-14 |
| H-07 | Retrieval/RAG per meeting | P1 | TERBLOKIR KEPUTUSAN | Keputusan DEC-11; hanya bila sliding context tidak cukup |
| H-08 | Ringkasan meeting | P1 | TERBLOKIR KEPUTUSAN | Keputusan DEC-15 |
| H-09 | Action items | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-15 |
| H-10 | Check Sumopod | P1 | PERLU VALIDASI | `-check sumopod` lulus dengan key/model nyata |
| H-11 | Gaya jawaban dan bahasa | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-16 |
| H-12 | Deteksi bahasa pertanyaan | P2 | BELUM DIMULAI | Bahasa pertanyaan terdeteksi sebelum prompt disusun; fallback aman saat deteksi gagal |

### I. Antarmuka pengguna

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| I-01 | Popup auth (login/register/logout) | P0 | SELESAI LOKAL | Alur auth berfungsi; status sesi tampil |
| I-02 | Popup status meeting | P1 | PERLU VALIDASI | Canonical URL, `meeting_id`, status sinkronisasi, retry |
| I-03 | Panel transcript live | P0 | SELESAI LOKAL | Transkrip streaming tampil di popup dari state `chrome.storage.session`: daftar final berurutan lalu partial terakhir, teks kosong diabaikan, dan status transkripsi punya pesan |
| I-04 | Panel terjemahan | P0 | BELUM DIMULAI | Terjemahan sejajar dengan segmen sumber |
| I-05 | Panel bantuan AI | P1 | BELUM DIMULAI | Pertanyaan terdeteksi dan jawaban AI terpisah jelas dari transkrip |
| I-06 | Halaman history meeting | P1 | BELUM DIMULAI | Daftar run milik pengguna |
| I-07 | Tampilan detail satu meeting | P1 | BELUM DIMULAI | Transkrip, terjemahan, ringkasan, ekspor |
| I-08 | Kontrol capture | P0 | SELESAI LOKAL | Mulai/stop capture dengan status jelas. Tombol "Mulai merekam"/"Berhenti merekam" ada di popup (`extension/src/App.jsx`) sejak F3; worker menambah run, menangkap tab, dan menutup run saat berhenti, tab ditutup, atau logout (`extension/src/audio-capture.js`), dengan status capture, jumlah chunk, dan total byte di popup. Bukti: 18 test di `extension/src/audio-capture.test.js` dan 6 test di `extension/src/background-runtime.test.js` lulus 2026-10-22, serta endpoint run/chunk/stop terbukti live di container nyata (run 201 → chunk 200 → stop 200 `stopped`). Klik tombol di Chrome nyata tetap bagian dari checklist manual M-07 |
| I-09 | Bahasa tujuan | P1 | BELUM DIMULAI | Selector bahasa persist per pengguna |
| I-10 | Empty/error/loading state | P1 | BELUM DIMULAI | Setiap permukaan punya state yang jelas; error tanpa token |
| I-11 | Aksesibilitas | P1 | BELUM DIMULAI | Focus order, label, `aria-live` pada streaming |
| I-12 | Dark/light theme | P2 | BELUM DIMULAI | Mengikuti preferensi sistem |
| I-13 | Bahasa UI | P2 | BELUM DIMULAI | Saat ini UI berbahasa Indonesia; lokalisasi belum jadi kebutuhan |
| I-14 | Konfigurasi dan panel pengaturan | P1 | BELUM DIMULAI | Preferensi pengguna non-secret |

### J. Integrasi provider dan keandalan

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| J-01 | Client Deepgram | P0 | SELESAI LOKAL | Client streaming dengan deadline handshake, timeout write, dan teardown berbatas waktu; klog SDK tidak diaktifkan sehingga payload audio tidak pernah dicatat. Retry/backoff belum ada: reconnect otomatis di luar cakupan F4 |
| J-02 | Client Sumopod | P0 | BELUM DIMULAI | Non-streaming untuk check, streaming untuk produksi bila perlu |
| J-03 | Konfigurasi model dari environment | P0 | SELESAI LOKAL | Model wajib diisi; tidak ada default tebakan |
| J-04 | Host provider dibatasi | P1 | SELESAI LOKAL | Sumopod base URL dibatasi host resmi |
| J-05 | Redundansi provider | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-17 |
| J-06 | Handling rate limit/quota | P1 | TERBLOKIR KEPUTUSAN | Backoff dihormati dan terlihat di UI |
| J-07 | Retry transkripsi saat reconnect | P0 | BELUM DIMULAI | Connection drop tidak mengulang segmen final |

### S. Keamanan, privasi, kepatuhan

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| S-01 | Secret hanya di backend | P0 | SELESAI LOKAL | Tidak ada secret di `extension/dist`, log, fixture, repo |
| S-02 | Larangan secret/service-role key | P0 | SELESAI LOKAL | Config menolak key itu sejak awal |
| S-03 | Token tidak keluar dari `storage.session` | P0 | SELESAI LOKAL | `TRUSTED_CONTEXTS`; `local` untuk URL metadata |
| S-04 | RLS owner-only end-to-end | P0 | SELESAI LOKAL | Uji dua akun pada Supabase nyata |
| S-05 | Ownership check pada semua endpoint meeting | P0 | SELESAI LOKAL | Token diverifikasi dan baris divalidasi pemilik |
| S-06 | Budget permission extension | P0 | PERLU VALIDASI | Izin: `storage`, `alarms`, `tabCapture`, `offscreen`; host terbatas ke provider meeting dan backend; tanpa `tabs` global, tanpa `audioCapture`, tanpa akses semua situs |
| S-07 | Sanitasi URL dan pembuangan secret URL | P0 | SELESAI LOKAL | Passcode/JWT/fragment tidak disimpan |
| S-08 | Sanitasi prompt dan output | P0 | BELUM DIMULAI | Data meeting tidak bocor ke prompt di luar run |
| S-09 | Rate limit API meeting | P1 | BELUM DIMULAI | Endpoint meeting dibatasi |
| S-10 | Threat model end-to-end | P1 | BELUM DIMULAI | Token, URL, audio, transcript, prompt, RLS ditinjau |
| S-11 | Kebijakan privasi dan consent | P0 | TERBLOKIR KEPUTUSAN | Keputusan DEC-05 dan DEC-18 |
| S-12 | Redaction data sensitif | P1 | TERBLOKIR KEPUTUSAN | Keputusan DEC-18 |

### L. Observabilitas dan operasi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| L-01 | Log terstruktur tanpa secret | P0 | BELUM DIMULAI | Field terstruktur; tidak ada token, connection string, body audio |
| L-02 | Health dan readiness terpisah | P1 | BELUM DIMULAI | Health liveness; readiness mencerminkan dependensi |
| L-03 | Metrik biaya token dan durasi | P1 | BELUM DIMULAI | Biaya provider terlihat tanpa data percakapan |
| L-04 | Trace ID korelasi | P2 | BELUM DIMULAI | Satu ID yang sama berlaku di request client, backend, dan provider |
| L-05 | Dokumentasi operator | P1 | BELUM DIMULAI | Deploy, migrasi, rotasi key, recovery |
| L-06 | Backup dan pemulihan database | P2 | TERBLOKIR KEPUTUSAN | Keputusan DEC-05 |

### M. Pengujian dan quality gate

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| M-01 | Test unit backend (`go test -race`) | P0 | SELESAI LOKAL | Semua paket lulus |
| M-02 | `go vet` bersih | P0 | SELESAI LOKAL | Tidak ada temuan |
| M-03 | Test extension (`node --test`) | P0 | SELESAI LOKAL | 104/104 lulus pada 2026-10-22 |
| M-04 | Contract test auth | P0 | SELESAI LOKAL | 7/7 lulus |
| M-05 | Test migrasi PostgreSQL sementara | P0 | SELESAI LOKAL | RLS, grants, idempotensi, identity stabil |
| M-06 | Fixture URL provider | P0 | SELESAI LOKAL | Fixture bersama frontend/backend |
| M-07 | Uji manual Chrome + provider nyata | P0 | PERLU VALIDASI | Checklist manual terdokumentasi di `docs/implementation/audio-status.md` bagian "Aktivasi manual (Chrome)"; sisi provider nyata sudah dijalankan 2026-10-22 (`-check deepgram` lulus, streaming live menghasilkan partial dan final), sisi Chrome belum karena lingkungan ini tidak punya binary Chrome |
| M-08 | Uji live Supabase | P0 | PERLU VALIDASI | Auth, profile, refresh, logout, resolve/list/detail meeting, race paralel, dan isolasi dua akun lulus pada project nyata; signup happy path tertahan rate limit email project |
| M-09 | Uji live provider berbayar | P1 | SELESAI LOKAL | Dijalankan eksplisit dengan key nyata pada 2026-10-22: `-check deepgram` → `deepgram: LULUS`, dan satu streaming live memakai key yang sama menghasilkan 3 partial + 1 final. Sumopod belum diuji karena di luar cakupan F4 |
| M-10 | Uji race & concurrency | P0 | SELESAI LOKAL | Resolve paralel, response basi, worker restart |
| M-11 | Uji offline/reconnect | P0 | BELUM DIMULAI | Semua jalur streaming pulih tanpa kehilangan data |
| M-12 | Uji data besar | P2 | BELUM DIMULAI | Meeting panjang tidak melampaui batas |
| M-13 | Uji provider outage | P1 | BELUM DIMULAI | Error jelas, tidak ada data rusak |
| M-14 | Uji upgrade migrasi | P1 | SELESAI LOKAL | Re-apply 0002 pada project nyata exit 0, data meeting utuh, policy tetap 2 |
| M-15 | Uji live API capture | P0 | SELESAI LOKAL | Create run 201, chunk 200 dengan akuntansi atomik, stop 200 idempoten, chunk setelah stop 409, tanpa token 401, meeting akun lain 404 |

### N. Rilis dan distribusi

| ID | Fitur | P | Status | Acceptance |
| --- | --- | --- | --- | --- |
| N-01 | Packaging extension siap distribution | P1 | BELUM DIMULAI | Build bersih, manifest valid, tanpa secret |
| N-02 | Dokumentasi pengguna | P1 | BELUM DIMULAI | Cara pakai, izin, batasan provider |
| N-03 | Environment deployment | P1 | BELUM DIMULAI | Backend jalan dengan konfigurasi nyata |
| N-04 | Checklist rilis | P1 | BELUM DIMULAI | Semua P0 selesai dan tervalidasi |
| N-05 | Rencana rollback | P2 | BELUM DIMULAI | Versi extension sebelumnya bisa dikembalikan |
| N-06 | Halaman Store listing | P2 | BELUM DIMULAI | Deskripsi, privasi, izin |

---

## 6. Permukaan data yang direncanakan

| Tabel | Isi | Milik | Status |
| --- | --- | --- | --- |
| `public.profiles` | Identitas dan `display_name` | Pengguna | Terapan (0001) |
| `public.meeting_sessions` | Identitas link meeting per pengguna | Pengguna | Terapan (0002) |
| `public.meeting_runs` | Metadata satu sesi capture (tanpa audio) | Pengguna | Terapan (0003) |
| `public.transcript_segments` | Segmen transkrip per run | Pengguna | TERBLOKIR KEPUTUSAN (DEC-05) |
| `public.translations` | Terjemahan per segmen | Pengguna | TERBLOKIR KEPUTUSAN (DEC-05) |
| `public.assistant_turns` | Pertanyaan dan jawaban AI | Pengguna | TERBLOKIR KEPUTUSAN (DEC-14) |
| `public.meeting_summaries` | Ringkasan per run | Pengguna | TERBLOKIR KEPUTUSAN (DEC-15) |

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
| POST | `/api/audio/runs` | Bearer | Selesai (F3) — terbukti live 2026-10-22 (201, run `active`) |
| POST | `/api/audio/runs/{id}/chunks` | Bearer | Selesai (F3) — terbukti live 2026-10-22 (200, akuntansi byte/chunk naik) |
| POST | `/api/audio/runs/{id}/stop` | Bearer | Selesai (F3) — terbukti live 2026-10-22 (200, `stopped`, idempoten) |
| POST | `/api/meetings/{id}/segments` | Bearer | Rencana (DEC-05) |
| GET | `/api/meetings/{id}/segments` | Bearer | Rencana (DEC-05) |
| GET | `/api/audio/runs/{id}/transcript` | Bearer | Selesai (F4) — SSE, event `segment`/`end`/`error`; terbukti live 2026-10-22 (3 partial + 1 final + `end`) |
| POST | `/api/transcription/sessions` | Bearer | Tidak dipakai; sesi transkripsi dibuat bersama run capture |
| WS/SSE | `/api/transcription/stream` | Bearer | Tidak dipakai; stream transkripsi memakai SSE di `/api/audio/runs/{id}/transcript` |
| POST | `/api/assistant/answer` | Bearer | Rencana (DEC-12) |
| GET | `/api/runs/{id}` | Bearer | Rencana (DEC-05) |
| DELETE | `/api/runs/{id}` | Bearer | Rencana (DEC-10) |

## 8. Urutan implementasi yang dijaga

```text
Validasi F1 Auth/Supabase nyata  ✓ live 2026-10-21 (sisa signup + Chrome)
  → Validasi F2 Meeting/Chrome nyata ✓ live 2026-10-21 (sisa Chrome)
  → DEC-01 + DEC-05 → F3 Audio Capture ✓ selesai 2026-10-21
  → F4 Streaming Transcription ✓ selesai lokal 2026-10-22; jalur server terbukti live 2026-10-22 (sisa validasi Chrome)
  → DEC-05 (penyimpanan transkrip) → F6 Storage & timeline
  → DEC-07 + DEC-08 (bahasa & provider) → F5 Translation
  → DEC-12 + DEC-13 (aktivasi & konteks) → F7 Assistant
  → F8 RAG hanya bila terbukti perlu
  → F9 History & ringkasan
  → F10 Hardening & rilis
```

Aturan: agent tidak boleh melompati dependency dengan mock, stub, no-op, atau fallback palsu. Spike eksplorasi hanya boleh jika dinyatakan throwaway dan disetujui pengguna.

## 9. Keputusan terbuka

Setiap keputusan mengunci fitur tertentu. Gunakan brainstorming/spec, lalu catat hasilnya di feature terkait dan hapus entry ini.

| ID | Keputusan | Mengunci | Batas default sementara |
| --- | --- | --- | --- |
| DEC-02 | Deteksi joined-state via DOM | C-21 | Deteksi berbasis URL saja |
| DEC-05 | Apa yang disimpan dan berapa lama untuk transkrip dan terjemahan | G-02…G-03, G-05…G-07, L-06 | Audio tidak disimpan (keputusan 2026-10-21). Penyimpanan transkrip dan terjemahan belum diputuskan |
| DEC-06 | Speaker diarization | E-10 | Tidak ada label speaker |
| DEC-07 | Bahasa sumber dan bahasa tujuan | F-01…F-03 | Tidak ada terjemahan |
| DEC-08 | Provider dan model terjemahan | F-01 | Tidak ada provider terjemahan |
| DEC-09 | Glosarium istilah | F-08 | Tanpa glosarium |
| DEC-10 | Hapus dan ekspor pengguna | G-07, N-04 | Tidak ada ekspor |
| DEC-11 | Kebutuhan RAG, chunking, embedding | H-07, G-09, G-10 | Sliding context saja; tanpa vector DB |
| DEC-12 | Aktivasi bantuan AI | H-01, H-02 | Tidak ada bantuan otomatis |
| DEC-13 | Konteks yang boleh dipakai AI | H-03, H-07 | Hanya run yang diminta |
| DEC-14 | Penyimpanan jawaban AI | H-06 | Jawaban tidak disimpan |
| DEC-15 | Artefak ringkasan dan action items | H-08, H-09, G-07 | Belum ada ringkasan |
| DEC-16 | Gaya dan bahasa jawaban AI | H-11 | Netral |
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
- Belum diuji end-to-end: signup sukses pada project nyata (rate limit email project), capture audio nyata di Chrome (tidak ada binary Chrome di lingkungan), streaming Deepgram dari browser (jalur server sudah terbukti live), Sumopod nyata.

### 11.1 Catatan operasional hasil validasi live

- Docker daemon dapat diakses; `docker compose up -d --build --remove-orphans backend` membangun image dan menjalankan backend pada port host 8989.
- Migrasi dapat diterapkan tanpa SQL Editor memakai image `postgres:18-alpine` dan `psql` dengan string koneksi dari `.env.before-supabase-api` (backup yang diabaikan Git). Skrip ini tidak pernah membaca `.env` aktif dan tidak mencetak kredensial.
- GoTrue mensyaratkan kolom string pada `auth.users` bernilai string kosong. Akun yang dibuat di luar GoTrue dengan `NULL` membuat password grant gagal 500 `Database error querying schema`; kolom yang harus `''`: `confirmation_token`, `email_change`, `email_change_token_current`, `email_change_token_new`, `recovery_token`, `reauthentication_token`, `phone_change`, `phone_change_token`.
- `raw_user_meta_data` akun non-GoTrue harus memuat `sub`, `email`, `email_verified`, `phone_verified`, dan `display_name`, mengikuti bentuk yang ditulis GoTrue.
- Akun uji yang ada di project: `livetranslate.e2e@livetranslate.invalid` dan `livetranslate.e2e2@livetranslate.invalid` (domain tidak dapat delivering, jadi tidak ada email keluar). Hapus lewat dashboard atau `DELETE FROM auth.users WHERE email LIKE 'livetranslate.e2e%'` bila tidak diperlukan lagi; baris `public.profiles` dan `public.meeting_sessions` ikut terhapus oleh FK cascade.
- Project membatasi pengiriman email konfirmasi; signup yang berulang akan menerima 429 `over_email_send_rate_limit`. Semua request AI berbayar tetap ditahan.

Dokumen status rinci: `docs/implementation/auth-status.md`, `docs/implementation/meeting-status.md`, `docs/obsidian-pending/2026-10-19-implementation-analysis.md`.

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
