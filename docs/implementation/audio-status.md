# Audio Capture Meeting — status implementasi

Tanggal: 2026-10-21. Fitur F3 selesai secara lokal dan terverifikasi live di sisi backend/database. Fitur F4 (transkripsi streaming) selesai secara lokal pada 2026-10-22 dan jalur server-nya terverifikasi live pada 2026-10-22 terhadap container dan akun Deepgram nyata; hasil verifikasinya tercatat di bagian "Verifikasi F4" dan "Verifikasi live F4". Pemeriksaan manual Chrome belum dijalankan karena lingkungan ini tidak memiliki binary Chrome.

## Keputusan yang dipakai

Empat keputusan pengguna mengunci desain:

| Keputusan | Hasil |
| --- | --- |
| Sumber audio | Audio tab meeting saja; mikrofon pengguna tidak diakses |
| Persetujuan | Capture hanya berjalan setelah pengguna menekan tombol di popup |
| Retensi | Audio tidak disimpan; backend meneruskan dan membuang byte, hanya metadata run yang disimpan |
| Semantik meeting | `meeting_id` tetap identitas link; setiap capture membuat `meeting_run_id` baru |

Implikasi consent: extension tidak pernah menangkap audio tanpa aksi eksplisit pengguna. Popup menampilkan status capture dan jumlah chunk/byte.

## Perilaku

- Worker memilih meeting sendiri dari tab aktif yang sudah terdaftar. Popup tidak dapat mengarahkan capture ke meeting lain.
- Capture hanya boleh dimulai dari tab yang statusnya `registered`; selain itu worker menolak dengan pesan yang jelas.
- Capture memakai `chrome.tabCapture.getMediaStreamId` + dokumen offscreen (`USER_MEDIA`). Frame diambil dari `AudioWorklet` sebagai PCM16 mono (bukan `MediaRecorder`), lalu dirangkai menjadi frame 100 ms dan diunggah berurutan sebagai body biner. `MediaRecorder` sudah dihapus dari `extension/src/offscreen.js`.
- Sample rate `AudioContext` dikirim saat membuat run dan diteruskan ke Deepgram sebagai `encoding=linear16`.
- Potongan audio diunggah berurutan ke `POST /api/audio/runs/{id}/chunks`; antrean upload satu per satu mencegah penumpukan. Maksimal 50 frame (5 detik) menunggu backend; frame yang lebih baru dibuang saat antrean penuh.
- Byte audio tidak lagi dibuang di backend: byte diteruskan ke sesi streaming Deepgram milik run tersebut, lalu byte-nya sendiri tidak disimpan.
- Backend menghitung byte dan chunk secara atomik di database lewat fungsi `append_audio_chunk`, sehingga chunk paralel tidak saling menimpa.
- Audio tidak pernah ditulis ke storage: byte diukur lalu diteruskan ke provider, lalu dibuang. Transkrip juga tidak ditulis ke storage; backlog segmen hanya hidup di memori backend.
- Capture berhenti saat pengguna menekan "Berhenti merekam", tab meeting ditutup, pengguna logout, terjadi error, atau transkripsi gagal.
- Respons 401 saat upload memicu permintaan token baru ke worker satu kali, lalu chunk dikirim ulang.

## Kontrak API

| Method | Path | Auth | Hasil |
| --- | --- | --- | --- |
| POST | `/api/audio/runs` | Bearer | 201 `{run}`; body `{"meeting_id":"<uuid>","sample_rate":48000}`; 400 bila `sample_rate` tidak positif; 503 `deepgram_config` bila konfigurasi provider kosong; 502 `deepgram_unreachable` bila sesi provider gagal dibuka (run ditandai `failed`) |
| POST | `/api/audio/runs/{id}/chunks` | Bearer | 200 `{run}`; body biner, `application/octet-stream`; 409 bila run sudah ditutup |
| POST | `/api/audio/runs/{id}/stop` | Bearer | 200 `{run}`; idempoten; memfinalisasi sesi provider dulu |
| GET | `/api/audio/runs/{id}/transcript` | Bearer | SSE; query `?since=<seq>`; event `segment`, `end`, `error`; 404 bila run tidak ada atau milik akun lain; 503 `transcript_unavailable` bila run aktif tanpa sesi live |

Kolom run: `id`, `user_id`, `meeting_id`, `status` (`active`/`stopped`/`failed`), `bytes_received`, `chunks_received`, `started_at`, `ended_at`.

## Batas

- Transkripsi sudah aktif (F4): byte audio diteruskan ke Deepgram dari backend. Yang belum ada adalah penyimpanan transkrip (DEC-05), terjemahan, dan bantuan AI.
- Batas ukuran: 2 MiB per chunk, 1 GiB per run.
- Izin extension bertambah: `tabCapture` dan `offscreen`. Tidak ada `audioCapture` dan tidak ada akses mikrofon.
- Audio hanya dapat ditangkap pada halaman yang ada di `host_permissions` (Google Meet, Zoom, Jitsi) dan backend.

## Temuan spike 2026-10-21 yang memengaruhi F3

Spike streaming Deepgram (sementara, sudah dihapus) membuktikan dua hal yang membatalkan asumsi format F3:

- `MediaRecorder` menghasilkan container webm/opus. Container yang dialirkan ke Deepgram **tidak pernah menghasilkan segmen final**; hanya partial, bahkan 20 detik setelah `Finalize()`.
- PCM16 mentah (`encoding=linear16`) dan µ-law menghasilkan partial maupun final secara konsisten.

Karena itu capture diubah dari `MediaRecorder` ke `AudioWorklet` yang mengirim PCM16 mono; perubahan ini sudah dikerjakan pada F4. Detail dan alasannya ada di `docs/superpowers/specs/2026-10-21-audio-transcription-design.md`. Implementasi capture PCM16 adalah bagian dari F4, bukan pembenihan F3.

## File utama

- `extension/src/audio-capture.js`: orkestrasi capture di worker (run, stream id, dokumen offscreen, status).
- `extension/src/offscreen.js`: capture PCM16 lewat `AudioWorklet`, unggah frame 100 ms, dan klien SSE transkrip.
- `extension/src/pcm16.js` dan `extension/public/pcm-worklet.js`: mixdown mono dan frame PCM16 little-endian.
- `extension/src/transcript-stream.js`: parser SSE dan klien stream transkrip (refresh token sekali saat 401).
- `extension/src/transcript-state.js`: state transkrip di `chrome.storage.session` (`transcript_state`).
- `extension/offscreen.html`: dokumen offscreen.
- `extension/src/background-runtime.js`: routing pesan popup dan offscreen, penghentian saat logout atau tab ditutup, penghentian capture saat transkripsi gagal.
- `extension/src/App.jsx`: tombol Mulai/Berhenti, indikator chunk/byte, dan daftar segmen transkrip.
- `extension/public/manifest.json`: izin `tabCapture` dan `offscreen`.
- `backend/internal/audio/{handler,repository,model,sse}.go`: endpoint run, chunk, stop, dan stream SSE transkrip.
- `backend/internal/transcribe/{provider,deepgram,session,broker,segment}.go`: klien Deepgram, sesi streaming per run, backlog segmen, dan subscriber SSE.
- `migration/0003_meeting_runs.sql`: tabel `meeting_runs`, RLS owner-only, fungsi atomik `append_audio_chunk` dan `stop_audio_run`.

## Verifikasi

| Pemeriksaan | Status | Bukti/batas |
| --- | --- | --- |
| `go test -race -count=1 ./...` | LULUS | Paket `internal/audio`: lifecycle run, akuntansi chunk, batas ukuran, chunk setelah stop, isolasi akun |
| `go vet ./...` | LULUS | Tidak ada temuan |
| `npm test` di extension | LULUS | 49/49 pada 2026-10-21; 104/104 pada 2026-10-22 setelah penambahan transkripsi, termasuk test parser SSE dan state transkrip |
| `npm run build` di extension | LULUS | Vite menghasilkan `dist/offscreen.html`, `dist/background.js`, popup, dan manifest |
| Migrasi 0003 di project nyata | LULUS | Tabel, 3 policy owner-only, 2 fungsi `SECURITY INVOKER`, `anon` tanpa EXECUTE; re-apply exit 0 |
| Harness migrasi lokal | LULUS | `sh migration/tests/check-meetings.sh`: apply dua kali, akuntansi byte/chunk, stop idempoten, chunk setelah stop ditolak, isolasi akun lain, `anon` ditolak. Balapan startup diperbaiki dengan probe koneksi nyata dan satu sesi psql; probe memakai TCP karena server bootstrap entrypoint hanya mendengarkan socket, sehingga probe hanya bisa mencapai server yang tetap hidup (2026-10-22, 4/4 jalan berturut-turut) |
| API capture live | LULUS | Create run 201, chunk 200 (11 byte → 22 byte, 1 → 2 chunk), stop 200 dengan `ended_at`, chunk setelah stop 409, tanpa token 401, meeting akun lain 404 |
| Baris run di database | LULUS | `stopped|22|2|t` |
| Capture audio nyata di Chrome | BELUM DIUJI | Tidak ada binary Chrome di lingkungan ini; jalur diuji dengan API browser tiruan |
| Transkripsi (fase F3) | DI LUAR BATAS | Pada tahap F3 byte audio sengaja dibuang. Fitur F4 sekarang aktif; lihat bagian "Verifikasi F4" |

## Verifikasi F4 — 2026-10-22

Perintah berikut dijalankan di workspace ini pada 2026-10-22:

| Pemeriksaan | Perintah | Hasil | Bukti/batas |
| --- | --- | --- | --- |
| Test backend | `cd backend && go test ./...` | LULUS | Semua paket lulus, termasuk `internal/transcribe` (provider, broker, backlog, sesi) dan `internal/audio` (route run/chunk/stop/SSE) |
| Vet backend | `cd backend && go vet ./...` | LULUS | Tidak ada temuan |
| Test extension | `cd extension && npm test` | LULUS | 104/104 lulus, 0 gagal: parser SSE, klien stream, state transkrip, PCM16, capture, dan worker |
| Build extension | `cd extension && npm run build` | LULUS | Vite 7.3.7 menghasilkan `dist/offscreen.html`, `dist/background.js`, popup, dan manifest |
| Contract test auth | `node --test --test-isolation=none http/auth.test.mjs` | LULUS | 7/7 lulus |
| Compose | `docker compose config --quiet` | LULUS | Tidak ada error konfigurasi |
| Harness migrasi | `sh migration/tests/check-meetings.sh` | LULUS | `meeting run accounting and RLS checks passed` |
| Model dan bahasa Deepgram | Uji terhadap akun nyata (spike 2026-10-21) | LULUS | `nova-2`, `general`, dan `meeting` diterima; `2-general` dan `2-meeting` ditolak 400 "No such model/language/tier combination". Bahasa `multi`, `en-US`, dan `id` diterima; `id-ID` ditolak |
| Kesesuaian model/bahasa | `DEEPGRAM_MODEL=nova-2`, `DEEPGRAM_LANGUAGE=multi` | LULUS | Kedua nilai di `.env` dan `.env.example`; keduanya diterima akun |
| Format wire PCM16 | Streaming spike 2026-10-21 | LULUS | PCM16 mentah (`linear16`) menghasilkan segmen partial dan final; container webm/opus tidak pernah menghasilkan final |
| Streaming provider end-to-end dari browser | — | BELUM DIUJI | Memerlukan capture nyata di Chrome dan backend live; verifikasi manual dicatat di bagian "Aktivasi manual" |
| Capture + transkrip di Chrome nyata | — | BELUM DIUJI | Tidak ada binary Chrome di lingkungan ini |
| Penyimpanan audio dan transkrip | Pemeriksaan kode dan skema | LULUS | Tidak ada tabel atau kolom transkrip; `meeting_runs` hanya menyimpan status dan akuntansi byte/chunk; backlog segmen hanya di memori backend |
| Streaming Deepgram nyata dengan key | `docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav` | LULUS | Account nyata Deepgram, key dari `.env`; `deepgram: LULUS`. Fixture berupa sampel SDK, bukan data pengguna |

## Verifikasi live F4 — 2026-10-22 (container nyata, provider nyata)

Semua perintah di bawah dijalankan di workspace ini pada 2026-10-22 terhadap container nyata `livetranslate-init-backend-1` (image dibangun dari `backend/Dockerfile`, host port 8989), project Supabase nyata, dan akun Deepgram nyata. Audio yang dipakai adalah sampel SDK Deepgram (`deepgram-go-sdk/v3@v3.8.0/examples/speech-to-text/websocket/replay/testing.wav`, 4,68 detik suara manusia), bukan data pengguna. Fixture disalin ke `fixtures/check.wav`; berkas itu diabaikan Git.

### Suite lokal

| Pemeriksaan | Perintah | Hasil | Bukti |
| --- | --- | --- | --- |
| Test backend | `cd backend && go test -race -count=1 ./...` | LULUS | Semua paket `ok` (`cmd/server`, `internal/audio`, `internal/auth`, `internal/health`, `internal/meeting`, `internal/sumopod`, `internal/transcribe`), 0 gagal |
| Vet backend | `cd backend && go vet ./...` | LULUS | Tanpa temuan |
| Test extension | `cd extension && npm test` | LULUS | 104/104 lulus, 0 gagal |
| Build extension | `cd extension && npm run build` | LULUS | Vite 7.3.7 → `dist/offscreen.html`, `dist/background.js`, popup, manifest |
| Contract test auth | `node --test --test-isolation=none http/auth.test.mjs` | LULUS | 7/7 lulus |
| Compose | `docker compose config --quiet` | LULUS | Tanpa error konfigurasi |
| Harness migrasi | `sh migration/tests/check-meetings.sh` | LULUS | `meeting run accounting and RLS checks passed` |

### Backend nyata dan provider nyata

| Pemeriksaan | Perintah | Hasil | Bukti/batas |
| --- | --- | --- | --- |
| Build dan jalan container | `docker compose up -d --build backend` | LULUS | Image `livetranslate-init-backend`, container `Up`, port `127.0.0.1:8989->8080/tcp` |
| Health | `curl --fail http://127.0.0.1:8989/health` | LULUS | HTTP 200 `{"service":"livetranslate-backend","status":"ok"}` |
| Check provider Deepgram | `set -a; . ./.env; set +a` lalu `docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav` | LULUS | `deepgram: LULUS` (akun nyata, key nyata; satu panggilan berbayar yang diizinkan) |
| Login akun uji | `curl -X POST http://127.0.0.1:8989/api/auth/login -d '{"email":"livetranslate.e2e@livetranslate.invalid",...}'` | LULUS | HTTP 200, `session.access_token` dan `expires_at` kembali; kredensial tidak dicetak |
| Resolve meeting | `curl -X POST http://127.0.0.1:8989/api/meetings/resolve -d '{"canonical_url":"https://meet.google.com/abc-defg-hij"}'` | LULUS | HTTP 200, `meeting.id` = `b1f098d8-31b6-4f94-9a4c-c82a1192c703` |
| Buat run capture | `curl -X POST http://127.0.0.1:8989/api/audio/runs -d '{"meeting_id":"b1f098d8-31b6-4f94-9a4c-c82a1192c703","sample_rate":8000}'` | LULUS | HTTP 201, run `4d581221-2f05-4db9-863e-5d011350ebd5` berstatus `active`; sesi Deepgram terbuka |
| Upload chunk 1 (32.000 byte PCM16 8 kHz) | `curl -X POST http://127.0.0.1:8989/api/audio/runs/{id}/chunks --data-binary @chunk1.raw` | LULUS | HTTP 200, `bytes_received` 0 → 32.000, `chunks_received` 0 → 1 |
| Upload chunk 2 (42.880 byte) | `curl -X POST http://127.0.0.1:8989/api/audio/runs/{id}/chunks --data-binary @chunk2.raw` | LULUS | HTTP 200, `bytes_received` 32.000 → 74.880, `chunks_received` 1 → 2 |
| Stream transkrip SSE | `curl -N http://127.0.0.1:8989/api/audio/runs/{id}/transcript` | LULUS | Events `segment` (3 partial + 1 final, `seq` 1–4, `language` `en`) lalu `end`; teks hasil pengenalan suara pada sampel SDK |
| Stop run | `curl -X POST http://127.0.0.1:8989/api/audio/runs/{id}/stop` | LULUS | HTTP 200, `status` `stopped`, `ended_at` terisi, `bytes_received` 74.880, `chunks_received` 2 |
| Chunk setelah stop | `curl -X POST .../chunks` | LULUS | HTTP 409 `run_closed` |
| Stop kedua kali | `curl -X POST .../stop` | LULUS | HTTP 200, run tetap `stopped` (idempoten) |
| Stream transkrip run yang sudah stop | `curl -N .../transcript` | LULUS | Hanya event `end`, tanpa error |
| Isolasi akun di run live | `curl -H "Authorization: Bearer <token akun kedua>" .../transcript` | LULUS | HTTP 404 `not_found`; run akun lain tidak terlihat |
| Baris run di Supabase nyata | `GET {SUPABASE_URL}/rest/v1/meeting_runs?id=eq.{run_id}` | LULUS | `stopped`, `bytes_received` 74.880, `chunks_received` 2; tidak ada kolom audio atau transkrip |
| Log container | `docker compose logs backend` | LULUS | Hanya `LiveTranslate backend mendengarkan :8080`; tidak ada secret, audio, atau teks transkrip |
| Capture + transkrip dari Chrome nyata | — | BELUM DIUJI | Tidak ada binary Chrome di lingkungan ini; `AudioWorklet`, `tabCapture`, dan popup belum dijalankan di browser |
| Streaming transkrip dari browser | — | BELUM DIUJI | Jalur SSE dan popup sudah lulus di test lokal; end-to-end browser tetap perlu validasi manual |

### Cara mereproduksi bagian audio

```bash
# Fixture pemeriksaan provider (diabaikan Git, disalin dari sampel SDK Deepgram)
cp "$(go env GOMODCACHE)/github.com/deepgram/deepgram-go-sdk/v3@v3.8.0/examples/speech-to-text/websocket/replay/testing.wav" fixtures/check.wav

# PCM16 mono 8 kHz untuk unggah chunk (format linear16 yang dipakai backend)
ffmpeg -y -i fixtures/check.wav -f s16le -acodec pcm_s16le -ar 8000 -ac 1 /tmp/pcm16_8k.raw
```

## Aktivasi manual (Chrome)

1. Terapkan `migration/0003_meeting_runs.sql` di project Supabase. **Selesai 2026-10-21.**
2. Build backend: `docker compose up -d --build backend`. **Selesai 2026-10-21.**
3. Build extension: `npm run build` di `extension/`, lalu Reload `extension/dist` pada `chrome://extensions`.
4. Login, buka ruang meeting yang didukung, pastikan popup menampilkan "Mulai merekam", lalu tekan tombolnya.
5. Periksa indikator capture, jumlah chunk bertambah, dan `meeting_runs` berisi baris `active` lalu `stopped`.
6. Tutup tab meeting saat capture berjalan; run harus berhenti otomatis.
7. Pastikan `.env` backend memuat `DEEPGRAM_API_KEY`, `DEEPGRAM_MODEL=nova-2`, dan `DEEPGRAM_LANGUAGE=multi`, lalu build ulang container backend. Tanpa itu pembuatan run gagal dengan 503 `deepgram_config`.
8. Saat ada suara, popup harus menampilkan segmen transkrip: status berubah dari "Menunggu suara pertama" ke "Transkrip live ditampilkan". Tutup lalu buka popup: transkrip tetap ada karena state disimpan di `chrome.storage.session`.
9. Tekan "Berhenti merekam"; status akhir menjadi "Transkrip selesai" dan `meeting_runs` menjadi `stopped`.
10. Kalau provider gagal, popup menampilkan "Transkripsi gagal." dan capture harus berhenti sendiri (status capture menjadi "Capture dihentikan.").
