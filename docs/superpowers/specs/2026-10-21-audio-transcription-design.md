# Desain transkripsi live LiveTranslate

Tanggal: 2026-10-21.
Status: rancangan untuk persetujuan pengguna; belum diimplementasikan.
Sumber lokal: docs/superpowers/specs/2026-10-21-audio-transcription-design.md.
Dasar: docs/implementation/feature-roadmap.md (fase F4), docs/implementation/audio-status.md (F3), hasil spike streaming Deepgram 2026-10-21.

## Tujuan dan cakupan

Dari audio tab meeting yang sudah tertangkap pada F3, LiveTranslate menghasilkan transkrip live berbahasa otomatis yang tampil di popup dan dapat dibaca pengguna.

Tujuan:

- Audio yang masuk ke backend diteruskan ke Deepgram secara streaming.
- Segmen transkrip partial dan final sampai ke extension dan tampil di popup.
- Pengguna selalu tahu status transkripsi: berjalan, gagal, atau berhenti.
- Tidak ada data meeting yang disimpan: audio maupun transkrip tidak ditulis ke database.

Tidak termasuk (di luar batas fitur ini):

- Penyimpanan transkrip atau terjemahan. `DEC-05` tetap terbuka; penyimpanan adalah fase F6.
- Terjemahan (F5) dan pemilihan bahasa tujuan.
- Diarization pembicara, ringkasan, RAG, dan asisten AI (F7–F9).
- Reconnect otomatis ke Deepgram dan pengiriman ulang transkrip.
- Pengambilan audio dari mikrofon pengguna. Sumber audio tetap tab meeting.

## Fakta yang sudah diverifikasi (spike 2026-10-21)

Fakta berikut diuji langsung terhadap akun Deepgram milik pengguna, bukan diambil dari dokumentasi.

| Fakta | Bukti |
| --- | --- |
| Model yang diterima akun: `nova-2`, `general`, `meeting` | Prerecorded 200 dengan transkrip untuk ketiganya |
| Model `2-general` dan `2-meeting` **ditolak** akun | 400 "No such model/language/tier combination" |
| `/v1/models` adalah katalog publik, bukan daftar kemampuan akun | 200 juga dengan token palsu; jangan dipakai untuk menyimpulkan ketersediaan model |
| Auth streaming harus lewat header `Authorization: Token <key>` | Header → handshake berhasil; query `?token=` → 401 `INVALID_AUTH`; SDK sudah mengirim header yang benar |
| Streaming `nova-2` berhasil | Partial dan final diterima lewat SDK v3.8.0 |
| Bahasa yang diterima: `multi`, `en-US`, `id`; `id-ID` ditolak | 200 untuk tiga yang pertama, 400 untuk `id-ID` |
| Deteksi bahasa tidak tersedia di streaming | Field `Languages[0]` kosong pada sebagian konfigurasi |
| **PCM16 mentah (linear16) dan µ-law menghasilkan segmen final** | `final=true` diterima pada keduanya |
| **Container webm/opus tidak pernah menghasilkan segmen final** | 20 detik setelah `Finalize()` tetap hanya partial; container tidak pernah ditutup |
| Bahasa hasil segmen | `Languages[0]` terisi `en` pada konfigurasi PCM16 dan container, kosong pada µ-law |

Konsekuensi: **format wire harus PCM16 mentah**, bukan hasil `MediaRecorder`. Ini mengubah bagian F3 (lihat "Amandemen F3").

## Keputusan yang sudah diambil

| Keputusan | Nilai | Dampak teknis |
| --- | --- | --- |
| Model Deepgram | `nova-2` | Multibahasa; streaming dan prerecorded; harus diisi di `DEEPGRAM_MODEL` |
| Bahasa | `multi` sebagai default, dapat diubah lewat `DEEPGRAM_LANGUAGE` | `multi` diterima di streaming dan mencakup beberapa bahasa sekaligus |
| Format wire | PCM16 mono, sample rate sesuai konteks audio | `encoding=linear16`, `sample_rate` dari run |
| Penyimpanan transkrip | Tidak | Tabel segmen belum dibuat; backlog hanya di memori backend |
| Transport ke extension | Server-Sent Events | `net/http` standar; tanpa server WebSocket tanpa library |
| Layar | Popup | Tidak ada permukaan UI baru |
| Sumber audio | Tab meeting | `tabCapture` plus dokumen offscreen, sudah ada di F3 |
| Persetujuan | Tombol "Mulai merekam" | Capture hanya berjalan atas aksi pengguna |
| Retensi audio | Tanpa penyimpanan | Byte audio diteruskan ke Deepgram lalu dibuang |

## Amandemen F3 (wajib dikerjakan bersama F4)

Dokumen offscreen F3 memakai `MediaRecorder` sehingga menghasilkan container webm/opus. Container itu tidak menghasilkan segmen final, sehingga harus diganti:

- Audio diambil dengan `getUserMedia` dari stream `tabCapture` seperti sekarang.
- Frame PCM16 diambil dengan `AudioWorklet` (bukan `MediaRecorder`), digabung menjadi potongan 100 ms, lalu diunggah berurutan.
- Audio downmix ke mono; sample rate mengikuti `AudioContext.sampleRate` dan dikirim saat membuat run.
- `MediaRecorder` dihapus dari `extension/src/offscreen.js`.
- Worklet berada di `extension/public/pcm-worklet.js` (disalin utuh ke `dist` oleh Vite, nama tetap sehingga URL dapat dipanggil dengan `chrome.runtime.getURL`).

Roadmap D-07 harus diperbarui: format capture adalah PCM16 mentah, bukan webm/opus.

## Arsitektur

```text
offscreen (AudioWorklet -> PCM16 100 ms)
  -> POST /api/audio/runs/{id}/chunks  (biner, Bearer)
       -> transcribe.Session.Write(bytes)  -> Deepgram WebSocket streaming
       -> akuntansi byte/chunk di meeting_runs (tetap, tidak menyimpan audio)

Deepgram MessageResponse (partial/final)
  -> transcribe.Broker (backlog 100 segmen + subscriber per run)
  -> GET /api/audio/runs/{id}/transcript (SSE)
       -> dokumen offscreen (fetch streaming + parser SSE)
            -> chrome.storage.session (transcript_state)
                 -> popup (daftar segmen)
```

Batas tanggung jawab:

- Extension tidak pernah bicara langsung ke Deepgram. Key hanya di backend.
- Backend tidak menulis audio maupun transkrip ke storage. `meeting_runs` tetap hanya metadata.
- Dokumen offscreen adalah pemilik stream SSE selama capture berjalan, karena dokumen itu hidup selama capture dan tidak ikut mati ketika service worker di-restart.
- Popup adalah tampilan saja.

## Paket backend baru

Paket `backend/internal/transcribe`:

| File | Tanggung jawab |
| --- | --- |
| `segment.go` | Tipe `Segment` dan `Result` |
| `provider.go` | Interface `Provider`/`Stream` dan implementasi Deepgram |
| `session.go` | Satu sesi streaming untuk satu run: tulis byte, finalisasi, tutup |
| `broker.go` | Peta run ke sesi, backlog segmen, subscriber |

Interface yang dikunci:

```go
type Segment struct {
    Seq      int     `json:"seq"`
    Type     string  `json:"type"` // "partial" atau "final"
    Text     string  `json:"text"`
    Start    float64 `json:"start"`
    Duration float64 `json:"duration"`
    Language string  `json:"language"`
}

type Result struct {
    Text     string
    IsFinal  bool
    Start    float64
    Duration float64
    Language string
}

type Stream interface {
    Write(payload []byte) error
    Finalize() error
    Close() error
    Results() <-chan Result
    Errors() <-chan error
}

type Provider interface {
    Open(ctx context.Context, sampleRate int) (Stream, error)
}

type Broker struct { /* ... */ }

func NewBroker(cfg config.Config, provider Provider) *Broker
func (b *Broker) Open(ctx context.Context, runID string, sampleRate int) (*Session, error)
func (b *Broker) Session(runID string) (*Session, bool)
func (b *Broker) Remove(runID string)
func (s *Session) Write(payload []byte) error
func (s *Session) Close() error
func (s *Session) Subscribe(since int) (<-chan Segment, func())
func (s *Session) Segments() []Segment
```

`NewBroker` dengan `provider == nil` membuat provider Deepgram dari konfigurasi. Test menyuntikkan provider tiruan, sehingga tidak perlu jaringan.

Aturan `Subscribe(since)`: server mengirim setiap segmen dengan `seq > since`, lalu melanjutkan streaming. `since` adalah `seq` terakhir yang sudah dimiliki klien, jadi `since = 0` berarti klien belum memiliki segmen apa pun dan menerima seluruh backlog yang tersedia. Aturan ini menggantikan kalimat lama "since 0 berarti hanya segmen baru", yang bertentangan dengan aturan `seq > since` dan dengan kebutuhan popup yang dibuka setelah capture berjalan agar tetap menerima backlog. Backlog maksimum 100 segmen. Kalau `since` lebih tua dari backlog, server mengirim backlog yang tersedia lalu melanjutkan streaming.

Sesi juga harus dapat melaporkan error terminal provider, supaya lapisan SSE dapat membedakan "run selesai" dari "provider gagal" dan mengirim event `end` atau `error` yang tepat. Aksesor untuk ini boleh ditambahkan pada sesi tanpa mengubah surface lain.

## Kontrak API

Semua endpoint memakai `Authorization: Bearer <access_token>`. Selain SSE, response memakai `Cache-Control: no-store`.

| Method | Path | Perilaku F4 |
| --- | --- | --- |
| POST | `/api/audio/runs` | Body `{"meeting_id":"<uuid>","sample_rate":48000}`. Urutan: buat baris run, buka sesi Deepgram, bila gagal tandai run `failed` (PATCH status) dan balas 502 `deepgram_unreachable`; bila `DEEPGRAM_API_KEY` atau `DEEPGRAM_MODEL` kosong balas 503 `deepgram_config` tanpa membuat baris |
| POST | `/api/audio/runs/{id}/chunks` | Byte diteruskan ke sesi Deepgram, bukan dibuang. Tetap 200 `{run}` dengan akuntansi byte dan chunk. 404 bila run tidak ada, 409 bila run sudah ditutup |
| POST | `/api/audio/runs/{id}/stop` | Memanggil `Finalize()` supaya segmen final terkirim, lalu menutup sesi dan menandai run `stopped`. Idempoten |
| GET | `/api/audio/runs/{id}/transcript` | SSE. Query `?since=<seq>`. Event `segment`, `end`, `error` |

`sample_rate` wajib ada dan harus positif; 400 bila tidak. Nilai ini diteruskan ke Deepgram sebagai `sample_rate` dengan `encoding=linear16`.

Bentuk event SSE:

```text
event: segment
data: {"seq":7,"type":"final","text":"selamat pagi semua","start":12.4,"duration":1.9,"language":"id"}

event: end
data: {"run_id":"..."}
```

Aturan SSE:

- Kepemilikan dicek lebih dulu: baris run dibaca lewat Data API sehingga RLS otomatis membatasi. Run milik akun lain atau run yang tidak ada → 404 `not_found`.
- Run ada tetapi tidak ada sesi live (backend restart): 503 `transcript_unavailable`.
- Run sudah `stopped`: 200 dengan event `end` segera, tanpa segmen baru.
- SSE memakai context request tanpa batas 10 detik dan respons memakai `http.ResponseController.SetWriteDeadline(time.Time{})` agar tidak dipotong `WriteTimeout` server. `ReadTimeout` tidak relevan karena request sudah selesai dibaca.
- Event `error` hanya berisi kode dan pesan generik. Body, credential, dan audio provider tidak pernah diteruskan.

## Perilaku extension

- Dokumen offscreen membuka SSE dengan `fetch` dan membaca body sebagai stream. `EventSource` tidak dipakai karena tidak tersedia di service worker maupun di dokumen offscreen yang dipakai sebagai konteks capture.
- Parser SSE ditulis sendiri: memisahkan event pada baris kosong, membaca `event:` dan `data:`, mengabaikan event lain dan baris kosong.
- State transkrip disimpan di `chrome.storage.session` dengan kunci `transcript_state`:

```json
{
  "run_id": "…",
  "status": "transcribing",
  "last_seq": 42,
  "finals": [{"seq": 40, "text": "…", "start": 1.2, "duration": 0.8}],
  "partial": {"seq": 42, "text": "…", "start": 2.0, "duration": 0.3}
}
```

- `finals` hanya berisi segmen final; `partial` berisi partial terbaru. Partial berikutnya menimpa partial sebelumnya, sehingga satu kalimat tidak tampil dua kali. Final menimpa partial yang sedang berjalan dan langsung masuk ke `finals`.
- Maksimal 200 final disimpan; yang terlama dibuang.
- `transcript_state` milik satu `run_id`. Run baru mengosongkan state lama.
- Token 401 saat membuka SSE: dokumen offscreen meminta token baru ke worker lewat pesan `audio.token` yang sudah ada, lalu membuka SSE lagi satu kali. Kegagalan kedua menandai status `transcript_error`.
- Popup membaca `transcript_state` dari `chrome.storage.session` dan menampilkan `finals` berurutan lalu `partial` terakhir. Teks kosong diabaikan.
- Saat run berhenti, worker mengirim `audio.capture.stop` ke dokumen offscreen; dokumen menutup recorder dan SSE, menandai status `transcribed`, dan menyimpan final terakhir.
- Bila service worker di-restart saat capture berjalan, dokumen offscreen tetap hidup dan stream SSE tetap berjalan. Worker yang di-restart membaca `audio_status` dari `chrome.storage.session` untuk tahu bahwa capture masih berjalan.

## Status yang terlihat pengguna

| Situasi | Status | Pesan singkat |
| --- | --- | --- |
| Capture berjalan, belum ada segmen | `waiting` | Menunggu suara pertama |
| Segmen diterima | `transcribing` | Transkrip live ditampilkan |
| Deepgram gagal | `transcript_error` | Transkripsi gagal. Capture dihentikan |
| Run ditutup | `transcribed` | Transkrip selesai |
| Backend mati | `transcript_unreachable` | Backend tidak dapat dijangkau |

## Konfigurasi

| Variabel | Nilai | Sumber |
| --- | --- | --- |
| `DEEPGRAM_API_KEY` | sudah terisi di `.env` | pengguna |
| `DEEPGRAM_MODEL` | `nova-2` | diuji tersedia di akun |
| `DEEPGRAM_LANGUAGE` | `multi` | diuji tersedia di akun |

`DEEPGRAM_MODEL` dan `DEEPGRAM_LANGUAGE` baru. Nilai kosong membuat pembuatan run gagal dengan 503 `deepgram_config`, bukan crash.

## Batasan dan biaya

- Streaming Deepgram berbayar per durasi audio. Capture hanya berjalan setelah pengguna menekan tombol, dan berhenti saat pengguna menekan tombol berhenti, tab ditutup, pengguna logout, atau transkripsi gagal.
- `meeting_runs` menyimpan akuntansi byte dan chunk, sehingga usage provider bisa diaudit tanpa menyimpan audio.
- Batas ukuran: 2 MiB per chunk, 1 GiB per run.
- Tidak ada request berbayar saat startup, saat build, atau saat test otomatis.

## Verifikasi

| Pemeriksaan | Bentuk bukti |
| --- | --- |
| Konversi respons Deepgram | Test Go: `IsFinal` menentukan `type`, kanal dan alternatif pertama dipakai, bahasa diambil dari `Languages[0]` bila ada |
| Provider tiruan | Test Go: provider tiruan menyuntikkan `Result` dan error tanpa jaringan |
| Broker dan backlog | Test Go: replay sesuai `since`, backlog dibatasi 100, unsubscribe menghentikan aliran, `Seq` bertambah per segmen |
| Handler run | Test Go: 503 tanpa konfigurasi provider dan tanpa baris run, 502 saat koneksi gagal dengan baris berstatus `failed`, 400 bila `sample_rate` tidak valid |
| Handler chunk dan stop | Test Go: chunk diteruskan ke sesi, chunk setelah stop ditolak, stop idempoten |
| SSE | Test Go: event `segment`, `end`, `error`; 404 untuk run milik akun lain; 503 saat run aktif tanpa sesi live; 200 dengan `end` untuk run yang sudah berhenti |
| Parser SSE extension | Test JS: event `segment` terparse, event lain diabaikan, potongan body diabaikan |
| State transkrip extension | Test JS: final menimpa partial, partial menimpa partial, state terisolasi per `run_id`, 401 memicu permintaan token baru |
| Provider nyata | `-check deepgram` memakai `DEEPGRAM_MODEL=nova-2`; bukti streaming sudah ada dari spike 2026-10-21 (partial dan final diterima) |
| Chrome nyata | Checklist manual: capture berjalan, segmen tampil di popup, popup ditutup lalu dibuka kembali tidak menghilangkan transkrip, tab ditutup menghentikan run. Tidak dapat dijalankan di lingkungan tanpa binary Chrome |

## Yang dibiarkan ke builder

- Penamaan file, fungsi internal, dan pembagian helper di dalam paket `transcribe`.
- Format pesan error dalam bahasa Indonesia, asal tidak membocorkan detail provider.
- Cara parsing SSE di extension, asal memenuhi perilaku di atas.
- Detail worklet PCM16, asal menghasilkan PCM16 mono little-endian dengan sample rate yang diklaim.
- Test Go memakai `httptest` dan transport tiruan, mengikuti pola `backend/internal/audio/handler_test.go`.

## Yang tidak boleh diasumsikan

- Jangan memakai model yang tidak diuji terhadap akun ini. `2-general` dan `2-meeting` ditolak.
- Jangan memakai `GET /v1/models` untuk menyimpulkan ketersediaan model; endpoint itu publik.
- Jangan mengirim container webm/opus; segmen final tidak pernah muncul.
- Jangan menyimpan audio atau transkrip di database, log, atau file.
- Jangan memakai `EventSource`.
- Jangan menambahkan modul Go atau paket npm baru.
- Jangan mengirim request berbayar di luar capture yang dimulai pengguna dan pemeriksaan provider eksplisit.
