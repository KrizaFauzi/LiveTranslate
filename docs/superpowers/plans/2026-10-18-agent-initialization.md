# Inisialisasi LiveTranslate — Handoff AI Agent

> **HISTORICAL — Superseded by 2026-10-24 execution planning.** This document describes the initialization-only baseline and earlier pgx direction. See `2026-10-24-execution-master.md` and its detail files for the current design. Contents are preserved for history; do not execute.

> Untuk AI Agent: kerjakan checklist berurutan. Jika tersedia, gunakan skill superpowers:executing-plans. Tugas ini hanya inisialisasi; berhenti setelah fondasi dan pemeriksaan service selesai.

**Tujuan:** Menyiapkan extension React, BE Go, Docker Compose, serta koneksi Supabase, Deepgram, dan Sumopod.  
**Arsitektur:** FE berjalan di Chrome. Satu service BE mengakses API dan database eksternal. Kode integrasi AIE berada di BE.  
**Stack:** JavaScript + React + Vite, Go standard library, SDK Go Deepgram, PostgreSQL driver pgx, API Sumopod, Docker Compose.  
**Acuan:** [[Tech Stack & Architecture]] dan PRD LiveTranslate. Jika diberikan tanpa kedua catatan tersebut, gunakan batas tugas dan keputusan dalam dokumen ini.

## 1. Batas Tugas

- Buat fondasi yang dapat dijalankan, bukan implementasi produk lengkap.
- Jangan membuat live audio capture, endpoint streaming, translation, chunking/RAG, skema meeting, atau deteksi pertanyaan pada tahap ini.
- Jangan menambah framework Go, SDK LLM, state manager, UI library, Redis, queue, atau service AI lokal.
- Jangan membuat akun/project cloud atau mengarang API key, password, model, dan ID project.
- Jangan mengirim request berbayar saat startup. Pemeriksaan API dijalankan eksplisit.
- Jika akses atau kredensial belum tersedia, selesaikan setup lokal dan laporkan pemeriksaan service sebagai BELUM DIUJI.

## 2. Periksa Workspace Dahulu

- [ ] Baca AGENTS.md yang berlaku, daftar file, dan status Git pada repository sebenarnya.
- [ ] Workspace yang diperiksa memiliki implementasi lama dalam `live_translate/`: FastAPI, React TypeScript, dan LibreTranslate. Root workspace bukan repository Git; verifikasi ulang saat eksekusi.
- [ ] Pertahankan implementasi lama. Untuk struktur baru di bawah, gunakan root workspace jika path belum ada; jika sudah ada, periksa lalu sesuaikan tanpa menimpa pekerjaan pengguna.
- [ ] Jangan menjalankan Compose lama atau mengunduh model LibreTranslate. Tugas baru memakai Go, extension JavaScript, serta API eksternal.

## 3. Instalasi & Service Penting

| Komponen | Instalasi / Konfigurasi |
|---|---|
| Docker Engine + Compose | Gunakan instalasi yang tersedia; verifikasi daemon dan `docker compose version` |
| Node.js + npm | Node 24 LTS disarankan untuk build FE; instalasi yang kompatibel boleh dipakai tanpa mengganti instalasi sistem |
| Go | Gunakan rilis stable yang kompatibel; builder Docker memakai versi yang sama dengan go.mod |
| Chrome | Memuat extension hasil build melalui Load unpacked |
| FE dependencies | Template Vite `react` (JavaScript), bukan `react-ts` |
| BE dependencies | SDK `github.com/deepgram/deepgram-go-sdk/v3` dan `github.com/jackc/pgx/v5/pgxpool` |
| Supabase | Project PostgreSQL managed dan connection string milik pengguna |
| Deepgram | API key; SDK Go mengakses service online |
| Sumopod | API key, model yang tersedia di akun, base URL `https://ai.sumopod.com/v1` |

Catat versi yang benar-benar digunakan. Simpan package-lock.json, go.mod, dan go.sum. Pin versi image yang diuji; jangan mengandalkan tag latest untuk hasil akhir.

Nama resmi provider LLM adalah **Sumopod**; pada diskusi awal tertulis “Sumapod”. Jika akun yang disediakan ternyata provider lain, laporkan ketidaksesuaian sebelum mengirim kredensial.

## 4. File yang Dibuat

```text
extension/
  public/manifest.json
  src/App.jsx
  src/config.js
  index.html
  vite.config.js
  package.json
  package-lock.json
backend/
  cmd/server/main.go
  internal/config/config.go
  internal/httpapi/health.go
  internal/httpapi/health_test.go
  internal/services/supabase.go
  internal/services/deepgram.go
  internal/services/sumopod.go
  Dockerfile
  .dockerignore
  go.mod
  go.sum
fixtures/README.md
compose.yaml
.env.example
.gitignore
README.md
```

Boleh tambah file kecil jika dibutuhkan. Jangan membuat folder kosong untuk fitur di luar inisialisasi.

## 5. Checklist Implementasi

### A. BE dan konfigurasi

- [ ] Inisialisasi module Go lokal bernama `livetranslate/backend`.
- [ ] Tambahkan SDK Deepgram dan pgx; gunakan `net/http` + `encoding/json` untuk Sumopod, tanpa SDK LLM tambahan.
- [ ] Implementasikan `GET /health`: HTTP 200, JSON `{"status":"ok","service":"livetranslate-backend"}`. Endpoint ini hanya liveness, tidak memanggil service eksternal.
- [ ] Server mendengarkan `:8080`. Startup tetap berhasil saat kredensial eksternal kosong.
- [ ] Baca konfigurasi dari environment. Validasi konfigurasi provider ketika pemeriksaan service dipanggil; error cukup menyebut nama konfigurasi yang kurang.
- [ ] Gunakan timeout koneksi/database 10 detik dan request AI 30 detik. Jangan mencatat secrets, connection string, atau body audio ke log.

Contoh `.env.example` di root:

```dotenv
APP_PORT=8080
DATABASE_URL=
DEEPGRAM_API_KEY=
DEEPGRAM_MODEL=
SUMOPOD_BASE_URL=https://ai.sumopod.com/v1
SUMOPOD_API_KEY=
SUMOPOD_MODEL=
```

Connection string Supabase harus diambil dari dashboard pengguna dengan SSL aktif. Direct connection untuk jaringan yang mendukungnya; session pooler jika deployment hanya IPv4. Tidak perlu memasang server PostgreSQL atau Supabase CLI untuk tahap ini.

### B. FE extension

- [ ] Buat template JavaScript React melalui `npm create vite@latest extension -- --template react`, hanya jika folder tujuan aman untuk dibuat.
- [ ] Buat manifest V3 dengan popup `index.html` dan host permission `http://localhost:8080/*`.
- [ ] Konfigurasikan Vite `base: "./"`; pastikan manifest ikut disalin ke dist.
- [ ] `src/config.js` menggunakan `import.meta.env.VITE_API_BASE_URL || "http://localhost:8080"`. Ini alamat publik BE, bukan secret.
- [ ] Popup hanya berisi judul LiveTranslate, tombol Periksa Backend, dan hasil GET /health. Tampilkan status gagal jika BE mati.
- [ ] Jangan tambahkan API key, credentials Supabase, permission capture, offscreen document, atau service worker sebelum fitur tersebut diimplementasikan.

### C. Docker Compose

- [ ] Buat multi-stage Dockerfile Go: download dependency, build binary `/app/server`, runtime non-root dengan sertifikat CA untuk HTTPS.
- [ ] ENTRYPOINT binary `/app/server`. Mode normal menjalankan HTTP server; flag pemeriksaan service dijelaskan pada bagian D.
- [ ] Compose hanya mempunyai service `backend`, build context `./backend`, `env_file: .env`, dan port `127.0.0.1:${APP_PORT:-8080}:8080`.
- [ ] Tetapkan Compose project name `livetranslate-init` agar terpisah dari project lama.
- [ ] Mount `./fixtures:/app/fixtures:ro` untuk pemeriksaan audio opsional. Jangan membuat service FE, database, Deepgram, Sumopod, atau LibreTranslate.
- [ ] Abaikan .env, node_modules, extension/dist, binary, dan fixtures audio pribadi dari Git/build context. Pertahankan .env.example dan fixtures/README.md.

### D. Integrasi service minimum

Implementasikan mode CLI pada binary yang sama: `-check supabase|deepgram|sumopod`. Mode ini menjalankan satu pemeriksaan lalu keluar, tanpa membuka HTTP server.

| Service | Pemeriksaan Eksplisit | Syarat Lulus |
|---|---|---|
| Supabase | pgxpool dengan DATABASE_URL, ping dan SELECT 1 | Koneksi berhasil, tanpa membuat tabel atau menyimpan data |
| Deepgram | SDK Go mentranskripsikan satu file audio kecil melalui mode prerecorded, parameter `-audio /app/fixtures/check.wav` | Respons sukses dan transkrip tidak kosong |
| Sumopod | POST ke BASE_URL + /chat/completions memakai model akun dan pesan “Reply with OK.”; non-streaming | Respons sukses dan pilihan jawaban berisi teks |

- [ ] SDK Deepgram harus benar-benar digunakan, bukan hanya dipasang tanpa pemanggilan.
- [ ] `DEEPGRAM_MODEL` dan `SUMOPOD_MODEL` wajib diisi sebelum pemeriksaan masing-masing; pilih dari dukungan provider/akun, bukan tebakan.
- [ ] Untuk Deepgram, gunakan audio fixture pengguna yang tidak sensitif. Jika tidak ada, laporkan belum diuji; jangan aktifkan mikrofon.
- [ ] Ikuti skema autentikasi resmi provider; jangan mencetak header Authorization atau respons sensitif.
- [ ] Setiap check mengembalikan exit 0 hanya jika sukses. Konfigurasi kosong, timeout, file hilang, dan penolakan provider menghasilkan exit nonzero serta pesan ringkas.
- [ ] Pemeriksaan Deepgram prerecorded hanya membuktikan integrasi SDK/API; belum membuktikan streaming atau kualitas live translation.

## 6. Cara Menjalankan yang Harus Tersedia

Dokumentasikan langkah dari root baru. Salin `.env.example` menjadi `.env` hanya jika .env belum ada; jangan menimpa isinya.

```bash
docker compose config --quiet
docker compose up --build -d backend
curl --fail http://localhost:8080/health
```

Jika APP_PORT diubah, sesuaikan URL BE pada FE dan host permission extension.

Build FE: jalankan `npm install` pertama kali untuk membuat lockfile, lalu `npm ci` untuk instalasi berikutnya dan `npm run build` dari folder extension. Buka chrome://extensions, aktifkan Developer mode, lalu Load unpacked folder extension/dist.

Pemeriksaan eksternal berikut hanya dijalankan setelah kredensial, model, dan fixture tersedia; request AI dapat memakai kuota:

```bash
docker compose run --rm backend -check supabase
docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav
docker compose run --rm backend -check sumopod
docker compose logs backend
docker compose down
```

## 7. Verifikasi & Hasil Akhir

- [ ] Tambahkan test handler /health: status 200 dan JSON sesuai, tanpa kredensial dan tanpa panggilan jaringan.
- [ ] Dari backend, `go test ./...` dan `go vet ./...` lulus.
- [ ] `docker compose config --quiet`, build BE, dan build FE lulus.
- [ ] Dengan semua kredensial kosong, BE tetap hidup dan /health merespons.
- [ ] Extension dimuat tanpa error CSP/asset; pemeriksaan BE sukses saat BE hidup dan menampilkan gagal saat BE mati.
- [ ] Check service tidak boleh melaporkan sukses jika konfigurasi kosong atau provider gagal.
- [ ] Tidak ada secrets di extension/dist, log, atau file yang akan masuk Git.
- [ ] Kode dan Compose lama tetap utuh.

Laporkan file yang dibuat, versi dependency/image, cara menjalankan, dan status masing-masing service: **LULUS / GAGAL / BELUM DIUJI**. Inisialisasi lokal boleh selesai meskipun pengujian eksternal masih menunggu credentials; jangan mengklaim integrasi eksternal terverifikasi tanpa bukti. Berhenti di sini, tanpa menambahkan fitur produk.

## Referensi Resmi

- [Vite React JavaScript template](https://github.com/vitejs/vite/tree/main/packages/create-vite/template-react).
- [Node.js releases](https://nodejs.org/en/about/previous-releases) dan [Go releases](https://go.dev/dl/).
- [SDK Go Deepgram](https://github.com/deepgram/deepgram-go-sdk).
- [Supabase PostgreSQL connection](https://supabase.com/docs/guides/database/connecting-to-postgres) dan [pgxpool](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).
- [Sumopod API](https://docs.sumopod.com/guides/ai/openai-compatible-api).
- [Chrome extension manifest](https://developer.chrome.com/docs/extensions/reference/manifest) dan [Docker Compose](https://docs.docker.com/compose/).

