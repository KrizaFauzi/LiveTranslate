# Transkripsi Live Meeting — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Mengubah capture audio F3 menjadi PCM16 dan mengirimkannya ke Deepgram secara streaming, lalu menampilkan transkrip partial dan final di popup tanpa menyimpan audio maupun transkrip.

**Architecture:** Dokumen offscreen menangkap PCM16 lewat `AudioWorklet`, mengunggah potongan 100 ms ke backend, dan membuka SSE ke `GET /api/audio/runs/{id}/transcript`. Backend meneruskan byte ke sesi streaming Deepgram per run, memublikasikan segmen ke backlog in-memory, laluңyalinya lewat SSE. State transkrip hidup di `chrome.storage.session` milik dokumen offscreen, sehingga tidak mati saat service worker di-restart.

**Tech Stack:** Go 1.27 standard library, SDK Deepgram `github.com/deepgram/deepgram-go-sdk/v3` v3.8.0 (sudah terpasang), React 19 + Vite 7, Chrome Manifest V3, `node --test` untuk test JavaScript.

**Spec:** docs/superpowers/specs/2026-10-21-audio-transcription-design.md

## Global Constraints

- Tidak ada modul Go baru dan tidak ada paket npm baru. Modul Go yang boleh dipakai: `github.com/deepgram/deepgram-go-sdk/v3` (sudah ada di `backend/go.mod`).
- Model Deepgram yang dipakai adalah `nova-2` dan bahasa `multi`. `2-general` dan `2-meeting` ditolak akun. `/v1/models` adalah katalog publik dan tidak boleh dipakai menyimpulkan ketersediaan model.
- Auth Deepgram lewat header `Authorization: Token <key>`. SDK sudah mengirimkannya. Query `?token=` ditolak akun.
- Audio tidak pernah ditulis ke database, log, atau file. Yang tersimpan hanya metadata run di `meeting_runs`.
- Transkrip tidak pernah ditulis ke database. Backlog hanya di memori backend; salinan di extension hanya di `chrome.storage.session`.
- Format wire: PCM16 mono little-endian, `encoding=linear16`, `sample_rate` sesuai `AudioContext.sampleRate` dan wajib dikirim saat membuat run.
- Tidak ada request berbayar saat startup, saat build, atau saat test otomatis. `-check deepgram` dijalankan eksplisit.
- Bahasa Indonesia pada pesan user-facing. Kode dan identifier memakai bahasa Inggris.
- Commit hanya file yang disebut pada task. Jangan memakai `git add -A`, jangan rewrite atau rebase history: working tree berisi pekerjaan sesi sebelumnya yang belum di-commit.
- Test Go memakai `httptest` dan transport tiruan, mengikuti `backend/internal/audio/handler_test.go`.
- Test JavaScript memakai `node --test` dan `node:assert/strict`, mengikuti `extension/src/audio-capture.test.js`.

## Review Focus

Lima kelas input atau kegagalan berikut tidak diuji oleh test mana pun pada task di bawah ini. Setiap baris harus mendapat test di task yang memiliki kodenya:

1. Chunk yang tiba setelah run ditutup atau setelah sesi Deepgram mati: handler harus menjawab 409 dan tidak boleh diam-diam membuang byte.
2. SSE dibuka untuk run yang sudah `stopped`: harus menjawab 200 dengan event `end`, bukan 404 atau 503.
3. Token access kedaluwarsa saat SSE dibuka: harus meminta token baru satu kali lalu membuka ulang; kegagalan kedua berstatus `transcript_error`.
4. SSE dibuka untuk run milik akun lain: harus 404 dan tidak boleh membocorkan segmen.
5. Hanya partial yang pernah masuk (deepgram tidak pernah mengirim final): partial terakhir harus tetap tampil dan state tidak boleh rusak.

---

### Task 1: Tipe bersama dan seam provider

**Files:**
- Create: `backend/internal/transcribe/segment.go`
- Create: `backend/internal/transcribe/provider.go`
- Test: `backend/internal/transcribe/segment_test.go`

**Interfaces:**
- Consumes: tidak ada. Task 1 tidak menyentuh paket `config`.
- Produces: `transcribe.Segment`, `transcribe.Result`, `transcribe.Stream`, `transcribe.Provider` — dipakai Task 2, 3, 4, 5.

- [ ] **Step 1: Write the failing test**

Buat `backend/internal/transcribe/segment_test.go`:

```go
package transcribe

import "testing"

func TestSegmentFromResult(t *testing.T) {
	segment := segmentFrom(Result{Text: "halo", IsFinal: true, Start: 1.5, Duration: 0.4, Language: "id"}, 7)
	if segment.Seq != 7 || segment.Type != "final" || segment.Text != "halo" || segment.Start != 1.5 || segment.Duration != 0.4 || segment.Language != "id" {
		t.Fatalf("unexpected segment: %+v", segment)
	}
	partial := segmentFrom(Result{Text: "ha", IsFinal: false}, 8)
	if partial.Type != "partial" {
		t.Fatalf("expected partial, got %q", partial.Type)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/transcribe/`
Expected: FAIL — `undefined: segmentFrom`.

- [ ] **Step 3: Implement the minimal code**

`backend/internal/transcribe/segment.go`:

```go
package transcribe

// Segment is one transcript update published to the extension.
type Segment struct {
	Seq      int     `json:"seq"`
	Type     string  `json:"type"`
	Text     string  `json:"text"`
	Start    float64 `json:"start"`
	Duration float64 `json:"duration"`
	Language string  `json:"language"`
}

// Result is a raw provider result before the broker assigns a sequence number.
type Result struct {
	Text     string
	IsFinal  bool
	Start    float64
	Duration float64
	Language string
}

func segmentFrom(result Result, seq int) Segment {
	kind := "partial"
	if result.IsFinal {
		kind = "final"
	}
	return Segment{Seq: seq, Type: kind, Text: result.Text, Start: result.Start, Duration: result.Duration, Language: result.Language}
}
```

`backend/internal/transcribe/provider.go`:

```go
package transcribe

import "context"

// Stream is one live provider connection.
type Stream interface {
	Write(payload []byte) error
	Finalize() error
	Close() error
	Results() <-chan Result
	Errors() <-chan error
}

// Provider opens provider streams. Tests inject a fake implementation.
type Provider interface {
	Open(ctx context.Context, sampleRate int) (Stream, error)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/transcribe/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transcribe/segment.go backend/internal/transcribe/provider.go backend/internal/transcribe/segment_test.go
git commit -m "feat(transcribe): add segment types and provider seam"
```

---

### Task 2: Provider Deepgram

**Files:**
- Create: `backend/internal/transcribe/deepgram.go`
- Test: `backend/internal/transcribe/deepgram_test.go`

**Interfaces:**
- Consumes: `transcribe.Stream`, `transcribe.Provider`, `transcribe.Result` (Task 1); `config.Config.DeepgramKey`, `config.Config.DeepgramModel`, `config.Config.DeepgramLanguage` (Task 6 menambahkan `DeepgramLanguage`, jadi pada task ini gunakan field yang sudah ada dan baca bahasa dari `os.Getenv("DEEPGRAM_LANGUAGE")` sementara).
- Produces: `newDeepgramProvider(cfg config.Config) Provider`, `liveOptions(model, language string, sampleRate int) *clientinterfaces.LiveTranscriptionOptions`, `toResult(message *msginterfaces.MessageResponse) Result`.

- [ ] **Step 1: Write the failing test**

Buat `backend/internal/transcribe/deepgram_test.go`:

```go
package transcribe

import (
	"testing"

	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
)

func TestLiveOptions(t *testing.T) {
	options := liveOptions("nova-2", "multi", 48000)
	if options.Model != "nova-2" || options.Encoding != "linear16" || options.SampleRate != 48000 {
		t.Fatalf("unexpected options: %+v", options)
	}
	if options.Language != "multi" {
		t.Fatalf("unexpected language: %q", options.Language)
	}
	if !options.InterimResults || !options.Punctuate || !options.SmartFormat {
		t.Fatalf("expected interim results, punctuation and smart format: %+v", options)
	}
}

func TestToResult(t *testing.T) {
	message := &msginterfaces.MessageResponse{
		IsFinal: true,
		Start:   2.5,
		Duration: 1.25,
		Channel: msginterfaces.Channel{Alternatives: []msginterfaces.Alternative{{
			Transcript: "selamat pagi",
			Languages:  []string{"id"},
		}}},
	}
	result := toResult(message)
	if result.Text != "selamat pagi" || !result.IsFinal || result.Start != 2.5 || result.Duration != 1.25 || result.Language != "id" {
		t.Fatalf("unexpected result: %+v", result)
	}
	empty := toResult(&msginterfaces.MessageResponse{})
	if empty.Text != "" || empty.IsFinal {
		t.Fatalf("unexpected empty result: %+v", empty)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/transcribe/`
Expected: FAIL — `undefined: liveOptions`, `undefined: toResult`.

- [ ] **Step 3: Implement the minimal code**

`backend/internal/transcribe/deepgram.go` memakai SDK:

```go
package transcribe

import (
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"os"
	"time"

	websocketv1 "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket"
	msginterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/websocket/interfaces"
	clientinterfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces"
	clientws "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen/v1/websocket"
)

const writeTimeout = 5 * time.Second

func liveOptions(model, language string, sampleRate int) *clientinterfaces.LiveTranscriptionOptions {
	return &clientinterfaces.LiveTranscriptionOptions{
		Model:          model,
		Language:       language,
		Encoding:       "linear16",
		SampleRate:     sampleRate,
		InterimResults: true,
		Punctuate:      true,
		SmartFormat:    true,
	}
}

func toResult(message *msginterfaces.MessageResponse) Result {
	result := Result{IsFinal: message.IsFinal, Start: message.Start, Duration: message.Duration}
	if len(message.Channel.Alternatives) > 0 {
		result.Text = message.Channel.Alternatives[0].Transcript
		if len(message.Channel.Alternatives[0].Languages) > 0 {
			result.Language = message.Channel.Alternatives[0].Languages[0]
		}
	}
	return result
}
```

Tambahkan `deepgramProvider` dan `deepgramStream` di berkas yang sama:

- `deepgramProvider.Open(ctx, sampleRate)` membuat `chans := websocketv1.NewDefaultChanHandler()`, `options := liveOptions(cfg.DeepgramModel, language, sampleRate)`, `client, err := clientws.NewUsingChan(ctx, cfg.DeepgramKey, &clientinterfaces.ClientOptions{}, options, chans)`. Bila `err != nil` atau `client == nil`, kembalikan error `konfigurasi Deepgram tidak valid`. Bila `!client.Connect()`, kembalikan error `koneksi Deepgram gagal`. Kembalikan `&deepgramStream{client: client, results: make(chan Result, 64), errors: make(chan error, 8), chans: chans}`.
- `deepgramStream.Write(payload)` memanggil `client.Write(payload)` di dalam goroutine dengan `context.WithTimeout(context.Background(), writeTimeout)`; ubah error menjadi `fmt.Errorf("Deepgram: penulisan gagal")` tanpa menyebut isi payload.
- `deepgramStream.Finalize()` memanggil `client.Finalize()`.
- `deepgramStream.Close()` memanggil `client.Finish()` lalu menutup kanal `results` dan `errors`.
- `deepgramStream.Results()` dan `Errors()` mengembalikan kanalnya.
- Satu goroutine membaca `*chans.GetMessage()[0]` dan menerjemahkan tiap pesan lewat `toResult` ke kanal `results`; goroutine lain membaca `*chans.GetError()[0]` dan menerjemahkan ke kanal `errors` dengan pesan generik `transkripsi Deepgram gagal` tanpa body provider.
- Bahasa diambil dari `os.Getenv("DEEPGRAM_LANGUAGE")`; bila kosong gunakan `multi`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/transcribe/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transcribe/deepgram.go backend/internal/transcribe/deepgram_test.go
git commit -m "feat(transcribe): add Deepgram streaming provider"
```

---

### Task 3: Broker dan sesi

**Files:**
- Create: `backend/internal/transcribe/session.go`
- Create: `backend/internal/transcribe/broker.go`
- Test: `backend/internal/transcribe/broker_test.go`

**Interfaces:**
- Consumes: `transcribe.Provider`, `transcribe.Stream`, `transcribe.Result`, `transcribe.Segment` (Task 1–2).
- Produces: `NewBroker(cfg config.Config, provider Provider) *Broker`, `(*Broker).Open(ctx, runID string, sampleRate int) (*Session, error)`, `(*Broker).Session(runID string) (*Session, bool)`, `(*Broker).Remove(runID string)`, `(*Session).Write(payload []byte) error`, `(*Session).Close() error`, `(*Session).Subscribe(since int) (<-chan Segment, func())`, `(*Session).Segments() []Segment`.

- [ ] **Step 1: Write the failing test**

Buat `backend/internal/transcribe/broker_test.go` dengan provider tiruan. Test ini memakai `config.Config{}`, jadi tambahkan import `livetranslate/backend/internal/config`:

```go
package transcribe

import (
	"context"
	"errors"
	"testing"
)

type fakeStream struct {
	writes  [][]byte
	results chan Result
	errors  chan error
	closed  bool
}

func (s *fakeStream) Write(payload []byte) error { s.writes = append(s.writes, payload); return nil }
func (s *fakeStream) Finalize() error            { return nil }
func (s *fakeStream) Close() error                { s.closed = true; return nil }
func (s *fakeStream) Results() <-chan Result      { return s.results }
func (s *fakeStream) Errors() <-chan error         { return s.errors }

type fakeProvider struct {
	stream *fakeStream
	err    error
}

func (p *fakeProvider) Open(context.Context, int) (Stream, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.stream, nil
}

func TestBrokerPublishesSegmentsInOrder(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, err := broker.Open(context.Background(), "run-1", 48000)
	if err != nil {
		t.Fatal(err)
	}
	segments, unsubscribe := session.Subscribe(0)
	defer unsubscribe()
	stream.results <- Result{Text: "halo", IsFinal: false}
	stream.results <- Result{Text: "halo dunia", IsFinal: true}
	first := <-segments
	second := <-segments
	if first.Seq != 1 || first.Type != "partial" || second.Seq != 2 || second.Type != "final" {
		t.Fatalf("unexpected segments: %+v %+v", first, second)
	}
	if len(session.Segments()) != 2 {
		t.Fatalf("unexpected backlog: %d", len(session.Segments()))
	}
}

func TestBrokerReplaysBacklogSince(t *testing.T) {
	stream := &fakeStream{results: make(chan Result, 4), errors: make(chan error, 1)}
	broker := NewBroker(config.Config{}, &fakeProvider{stream: stream})
	session, _ := broker.Open(context.Background(), "run-1", 48000)
	stream.results <- Result{Text: "satu", IsFinal: true}
	stream.results <- Result{Text: "dua", IsFinal: true}
	segments, unsubscribe := session.Subscribe(1)
	defer unsubscribe()
	replayed := <-segments
	if replayed.Seq != 2 || replayed.Text != "dua" {
		t.Fatalf("unexpected replay: %+v", replayed)
	}
}

func TestBrokerOpenFails(t *testing.T) {
	broker := NewBroker(config.Config{}, &fakeProvider{err: errors.New("gagal")})
	if _, err := broker.Open(context.Background(), "run-1", 48000); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/transcribe/`
Expected: FAIL — `undefined: NewBroker`, `undefined: config`.

- [ ] **Step 3: Implement the minimal code**

`backend/internal/transcribe/session.go`:

```go
package transcribe

import (
	"context"
	"sync"
)

const backlogLimit = 100

// Session is one live transcription for one run.
type Session struct {
	stream Stream
	mu     sync.Mutex
	seq    int
	backlog []Segment
	subscribers map[chan Segment]struct{}
	closed bool
}
```

- `NewSession(stream Stream) *Session` membuat map subscriber kosong.
- `Session.publish(result Result)` menambah `seq`, membentuk `Segment` lewat `segmentFrom`, menambahkannya ke backlog, dan mengirimkannya ke setiap subscriber yang tidak penuh (pakai `select` dengan `default` agar subscriber lambat tidak menahan publisher).
- Backlog dipangkas ke `backlogLimit` terbaru.
- `Session.Subscribe(since int) (<-chan Segment, func())` membuat kanal berkapasitas 32, mengirim seluruh backlog dengan `seq > since`, mendaftarkan kanal, dan mengembalikan fungsi unsubscribe yang menutup serta menghapus kanal.
- `Session.Segments()` mengembalikan salinan backlog.
- `Session.Write(payload []byte) error` meneruskan ke `stream.Write`.
- `Session.Close() error` menutup stream dan menutup semua kanal subscriber.

`backend/internal/transcribe/broker.go`:

```go
package transcribe

import (
	"context"
	"livetranslate/backend/internal/config"
	"sync"
)

type Broker struct {
	mu       sync.Mutex
	sessions map[string]*Session
	cfg      config.Config
	provider Provider
}

func NewBroker(cfg config.Config, provider Provider) *Broker {
	if provider == nil {
		provider = newDeepgramProvider(cfg)
	}
	return &Broker{sessions: map[string]*Session{}, cfg: cfg, provider: provider}
}

func (b *Broker) Open(ctx context.Context, runID string, sampleRate int) (*Session, error) {
	stream, err := b.provider.Open(ctx, sampleRate)
	if err != nil {
		return nil, err
	}
	session := NewSession(stream)
	b.mu.Lock()
	b.sessions[runID] = session
	b.mu.Unlock()
	return session, nil
}

func (b *Broker) Session(runID string) (*Session, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	session, ok := b.sessions[runID]
	return session, ok
}

func (b *Broker) Remove(runID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.sessions, runID)
}
```

`Session` harus menerima hasil dari stream provider: pada `NewSession`, jalankan satu goroutine yang membaca `stream.Results()` dan memanggil `publish`, dan goroutine lain yang membaca `stream.Errors()` lalu menutup sesi. `Session` menyimpan `errCh chan error` untuk melaporkan error itu; `Close()` menutup kanal itu.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/transcribe/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/transcribe/session.go backend/internal/transcribe/broker.go backend/internal/transcribe/broker_test.go
git commit -m "feat(transcribe): add broker with backlog and subscribers"
```

---

### Task 4: Integrasi handler audio

**Files:**
- Modify: `backend/internal/audio/handler.go`
- Modify: `backend/internal/audio/repository.go`
- Test: `backend/internal/audio/handler_test.go`

**Interfaces:**
- Consumes: `transcribe.Broker`, `transcribe.Session` (Task 3).
- Produces: `audio.NewHandler(c config.Config, client *http.Client, broker *transcribe.Broker) http.Handler`; `audio.RegisterRoutes(mux *http.ServeMux, c config.Config, broker *transcribe.Broker)`; body pembuatan run `{meeting_id, sample_rate}`; error codes `deepgram_config` (503) dan `deepgram_unreachable` (502).

- [ ] **Step 1: Write the failing test**

Tambahkan test berikut ke `backend/internal/audio/handler_test.go`:

```go
func TestRunRequiresSampleRateAndProvider(t *testing.T) {
	h, _ := newHandler(t)
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`"}`, "owner", "application/json"); w.Code != 400 {
		t.Fatalf("missing sample rate: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/audio/runs", `{"meeting_id":"`+meeting+`","sample_rate":48000}`, "owner", "application/json"); w.Code != 502 {
		t.Fatalf("provider failure: %d %s", w.Code, w.Body.String())
	}
}
```

Ubah helper `newHandler` agar menerima provider tiruan sebagai parameter, dengan dua varian: `newHandler(t, failingProvider{})` untuk menguji kegagalan provider, dan `newHandler(t, workingProvider{})` untuk pengujian alur normal. `workingProvider` mengembalikan stream tiruan yang menerima `Write` tanpa error sehingga run berhasil dibuat dan akuntansi chunk dapat diuji. Tambahkan import `livetranslate/backend/internal/transcribe` dan bangun `transcribe.NewBroker(config.Config{}, provider)` di dalam helper. Test yang sudah ada sebelumnya memakai `newHandler(t)`; sesuaikan pemanggilnya ke `newHandler(t, workingProvider{})` agar perilakunya tidak berubah.

Tambahkan test fokus dari bagian Review Focus: chunk setelah run ditutup harus 409 dan tidak boleh menambah akuntansi byte. Bentuk test: jalankan `POST /api/audio/runs/{id}/chunks` sekali saat run aktif, lalu `POST /api/audio/runs/{id}/stop`, lalu ulangi chunk; ekspektasi 409 `run_closed`, dan `bytes_received` pada run harus tetap sama seperti sebelum chunk yang ditolak.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/audio/`
Expected: FAIL — request tanpa `sample_rate` masih diterima (200) dan provider belum dipakai.

- [ ] **Step 3: Implement the minimal code**

Di `backend/internal/audio/handler.go`:

- `NewHandler` menerima parameter `broker *transcribe.Broker` dan menyimpannya di handler.
- Pada route `POST /api/audio/runs`, baca body JSON dengan dua field wajib `meeting_id` dan `sample_rate`. `sample_rate` harus integer positif; selain itu balas 400 `invalid_input`.
- Setelah baris run dibuat lewat repository, buka sesi: `session, err := broker.Open(ctx, run.ID, sampleRate)`. Bila `err != nil`: tandai run `failed` lewat `repo.markFailed(ctx, run.ID, user, token)`, balas 502 `deepgram_unreachable`, dan jangan simpan sesi di broker. Bila konfigurasi provider kosong (dicek di `transcribe`), balas 503 `deepgram_config` tanpa membuat baris run.
- Pada route `POST /api/audio/runs/{id}/chunks`: cari sesi lewat `broker.Session(runID)`. Bila tidak ada sesi dan run masih `active`, balas 409 `run_closed`. Bila ada sesi, panggil `session.Write(payload)` lalu lanjutkan akuntansi byte dan chunk seperti sekarang.
- Pada route `POST /api/audio/runs/{id}/stop`: bila sesi ada, panggil `session.Close()` (yang memanggil `Finalize()` lalu menutup stream) sebelum menandai run `stopped`, dan `broker.Remove(runID)` setelah penandaan.
- `deepgramProvider.Open` harus mengembalikan error berlabel agar handler bisa membedakan konfigurasi kosong dari kegagalan koneksi. Tambahkan `errors.Is`-friendly sentinel: `var ErrProviderConfig = errors.New("konfigurasi Deepgram belum lengkap")` di paket `transcribe`, dan `ErrProviderConnect` untuk kegagalan koneksi.

Di `backend/internal/audio/repository.go`:

- Tambahkan `func (repo repository) markFailed(ctx context.Context, id, user, token string) *failure` yang melakukan PATCH `status=failed` pada `meeting_runs` memakai kolom yang sudah di-grant.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/audio/ ./internal/transcribe/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/audio/handler.go backend/internal/audio/repository.go backend/internal/audio/handler_test.go
git commit -m "feat(audio): forward capture chunks to the transcription session"
```

---

### Task 5: Endpoint SSE transkrip

**Files:**
- Create: `backend/internal/audio/sse.go`
- Test: `backend/internal/audio/sse_test.go`

**Interfaces:**
- Consumes: `transcribe.Broker`, `transcribe.Segment` (Task 3), `repo.meetingOwned` dan `repo.get` (Task 4).
- Produces: route `GET /api/audio/runs/{id}/transcript`; event SSE `segment`, `end`, `error`.

- [ ] **Step 1: Write the failing test**

Buat `backend/internal/audio/sse_test.go`:

```go
package audio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscriptStreamOwnershipAndEvents(t *testing.T) {
	h, _ := newHandler(t)
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "owner", ""); w.Code != 404 {
		t.Fatalf("unknown run: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "other", ""); w.Code != 404 {
		t.Fatalf("foreign run: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "GET", "/api/audio/runs/"+runID+"/transcript", "", "owner", ""); w.Code != 503 {
		t.Fatalf("run without live session: %d %s", w.Code, w.Body.String())
	}
}
```

Tambahkan di `newHandler` sebuah run yang sudah `stopped` dan sebuah run `active` tanpa sesi, supaya dua kasus terakhir bisa dibedakan. Bila `newHandler` belum mendukung, tambahkan parameter penyetel run.

Tambahkan test fokus dari bagian Review Focus: SSE untuk run yang sudah `stopped` harus menjawab 200 dengan event `end` dan body yang memuat `event: end`.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/audio/`
Expected: FAIL — route belum terdaftar, sehingga respons 404 untuk semua kasus dan test gagal pada kasus run tanpa sesi.

- [ ] **Step 3: Implement the minimal code**

`backend/internal/audio/sse.go`:

```go
package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"livetranslate/backend/internal/transcribe"
	"net/http"
	"time"
)

func (h *handler) transcript(w http.ResponseWriter, r *http.Request) {
	// 1. identifikasi user lewat bearer token
	// 2. baca run lewat repo.get; bila tidak ada -> 404 not_found
	// 3. bila run.Status == "stopped" -> tulis header SSE lalu event "end" lalu return
	// 4. cari sesi di broker; bila tidak ada -> 503 transcript_unavailable
	// 5. subscribe dengan since dari query; tulis event "segment" untuk tiap segmen
	// 6. tutup stream bila context request selesai
}
```

Detail wajib:

- Response memakai `Content-Type: text/event-stream`, `Cache-Control: no-store`, `Connection: keep-alive`, dan `X-Accel-Buffering: no`.
- Context handler tidak memakai `context.WithTimeout(..., config.ConnectionTimeout)`. Pakai `r.Context()` langsung.
- Panggil `http.NewResponseController(w).SetWriteDeadline(time.Time{})` sebelum menulis event pertama supaya `WriteTimeout` server tidak memutus stream.
- Tulis event dengan format: `event: <nama>\ndata: <json>\n\n`.
- Event `segment` membawa `transcribe.Segment` apa adanya.
- Event `end` membawa `{"run_id":"<id>"}` dan menutup stream.
- Event `error` membawa `{"code":"transcript_error","message":"Transkripsi gagal."}` tanpa detail provider.
- Query `since` dibaca sebagai integer; nilai yang tidak valid diabaikan dan dianggap 0.
- Handler `audio` yang ada saat ini memakai closure `http.HandlerFunc`; refactor sekecil mungkin supaya `handler` menjadi struct yang menyimpan `repo`, `broker`, dan `verify` sehingga `transcript` dapat menjadi method.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/audio/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/audio/sse.go backend/internal/audio/sse_test.go backend/internal/audio/handler.go
git commit -m "feat(audio): stream transcript segments over SSE"
```

---

### Task 6: Konfigurasi dan wiring server

**Files:**
- Modify: `backend/internal/config/config.go`
- Modify: `backend/cmd/server/main.go`
- Modify: `backend/cmd/server/main_test.go`

**Interfaces:**
- Consumes: `transcribe.NewBroker` (Task 3), `audio.RegisterRoutes` signature baru (Task 4).
- Produces: `config.Config.DeepgramLanguage`; variabel env `DEEPGRAM_LANGUAGE`; route `POST /api/audio/runs` memakai `DEEPGRAM_MODEL` dan `DEEPGRAM_LANGUAGE` dari environment.

- [ ] **Step 1: Write the failing test**

Tambahkan ke `backend/cmd/server/main_test.go` test yang memanggil `newHandler(c)` dengan `config.Config{SupabaseURL: "https://project.supabase.co", SupabaseKey: "sb_publishable_test"}` lalu memastikan `GET /api/audio/runs/{uuid}/transcript` tanpa token menjawab 401 dan bukan 404.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./cmd/server/`
Expected: FAIL — route transkrip belum terdaftar pada mux.

- [ ] **Step 3: Implement the minimal code**

- `backend/internal/config/config.go`: tambahkan field `DeepgramLanguage string` dan isi dari `os.Getenv("DEEPGRAM_LANGUAGE")`.
- `backend/internal/transcribe/deepgram.go`: ganti pembacaan `os.Getenv("DEEPGRAM_LANGUAGE")` menjadi `cfg.DeepgramLanguage`, dan hapus import `os` bila tidak dipakai lagi. Default saat kosong adalah `multi`.
- `backend/cmd/server/main.go`: `newHandler(c config.Config)` membuat `broker := transcribe.NewBroker(c, nil)` lalu memanggil `audio.RegisterRoutes(mux, c, broker)`. Signature `newHandler` tetap satu parameter.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./... && go vet ./...`
Expected: PASS, tanpa temuan vet.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/config/config.go backend/cmd/server/main.go backend/cmd/server/main_test.go
git commit -m "feat(config): wire transcription broker into the server"
```

---

### Task 7: Capture PCM16 di extension

**Files:**
- Create: `extension/public/pcm-worklet.js`
- Modify: `extension/src/offscreen.js`
- Create: `extension/src/pcm16.test.js`

**Interfaces:**
- Consumes: `chrome.runtime.getURL('pcm-worklet.js')`, `AudioContext` di dokumen offscreen.
- Produces: potongan PCM16 mono little-endian 100 ms yang dikirim lewat fungsi upload yang sudah ada; `sample_rate` dikirim ke backend saat run dibuat.

- [ ] **Step 1: Write the failing test**

Buat `extension/src/pcm16.test.js` yang menguji fungsi helper PCM16 yang diekspor dari modul baru `extension/src/pcm16.js`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { toMonoPcm16, frameSizeFor } from './pcm16.js'

test('mono mixdown averages stereo frames', () => {
  const left = Float32Array.from([0.5, -0.5, 1, -1])
  const right = Float32Array.from([0.5, 0.5, 1, 1])
  const mono = toMonoPcm16([left, right], left.length)
  assert.equal(mono.length, 4)
  assert.equal(mono[0], 0x4000)
  assert.equal(mono[1], 0)
  assert.equal(mono[2], 0x7fff)
  assert.equal(mono[3], 0)
})

test('frame size follows the sample rate', () => {
  assert.equal(frameSizeFor(48000, 100), 4800)
  assert.equal(frameSizeFor(16000, 100), 1600)
})
```

`toMonoPcm16` mengembalikan `Int16Array` dengan nilai yang dibulatkan dari rata-rata kanal, dikali 32767.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd extension && node --test --test-isolation=none src/pcm16.test.js`
Expected: FAIL — modul `pcm16.js` belum ada.

- [ ] **Step 3: Implement the minimal code**

- `extension/src/pcm16.js`:

```js
export function toMonoPcm16(channels, length) {
  const out = new Int16Array(length)
  for (let i = 0; i < length; i += 1) {
    let sum = 0
    for (const channel of channels) sum += channel[i] ?? 0
    const value = Math.round((sum / channels.length) * 32767)
    out[i] = value > 32767 ? 32767 : value < -32768 ? -32768 : value
  }
  return out
}

export function frameSizeFor(sampleRate, milliseconds) {
  return Math.round((sampleRate * milliseconds) / 1000)
}
```

- `extension/public/pcm-worklet.js`: worklet yang menerima `process(inputs, outputs)` dari `AudioWorkletNode`, menggabungkan kanal menjadi mono dengan `toMonoPcm16` yang disalin di dalam berkas ini, lalu mem-posting `Float32Array` mono ke port. Worklet tidak boleh mengimpor modul; salin logikanya di dalam berkas.
- `extension/src/offscreen.js`: ganti `MediaRecorder` dengan `AudioContext` + `AudioWorkletNode`:
  - `context = new AudioContext({ sampleRate: 48000 })` bila `sampleRate` tidak didukung, biarkan default browser.
  - `await context.audioWorklet.addModule(chrome.runtime.getURL('pcm-worklet.js'))`.
  - `source = context.createMediaStreamSource(stream)`, `node = new AudioWorkletNode(context, 'pcm-capture')`, `source.connect(node)`, `node.connect(context.destination)` hanya bila diperlukan; lebih baik `node.connect(context.destination)` tetap menjaga graf audio tetap berjalan.
  - `node.port.onmessage` menerima frame mono; buffer frame hingga 100 ms (`frameSizeFor(context.sampleRate, 100)`), lalu kirim sebagai `Int16Array` lewat `postMessage` yang di-thread ke halaman utama.
  - Halaman utama mengonversi `Int16Array` menjadi `ArrayBuffer` dan mengunggah sebagai biner lewat `sendChunk` yang sudah ada. `Content-Type` tetap `application/octet-stream`.
  - `sample_rate` yang dipakai adalah `context.sampleRate` dan harus dikirim ke backend saat run dibuat (lihat Task 9 untuk plumbing pemanggilan run).
  - `stop()` menutup node, `context.close()`, dan menghentikan track stream.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd extension && node --test --test-isolation=none src/pcm16.test.js && npm test`
Expected: PASS, seluruh test extension hijau.

- [ ] **Step 5: Commit**

```bash
git add extension/public/pcm-worklet.js extension/src/pcm16.js extension/src/pcm16.test.js extension/src/offscreen.js
git commit -m "feat(extension): capture PCM16 frames with an audio worklet"
```

---

### Task 8: Parser SSE dan klien transkrip

**Files:**
- Create: `extension/src/transcript-stream.js`
- Create: `extension/src/transcript-stream.test.js`

**Interfaces:**
- Consumes: `fetch` global di dokumen offscreen, `chrome.runtime.sendMessage` untuk meminta token.
- Produces: `parseSseChunk(bufferText) -> Array<{event: string, data: string}>` dan `openTranscriptStream({url, token, onSegment, onEnd, onError, requestToken})`.

- [ ] **Step 1: Write the failing test**

Buat `extension/src/transcript-stream.test.js`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { parseSseChunk } from './transcript-stream.js'

test('parses segment and end events', () => {
  const events = parseSseChunk('event: segment\ndata: {"seq":1}\n\nevent: end\ndata: {"run_id":"x"}\n\n')
  assert.equal(events.length, 2)
  assert.equal(events[0].event, 'segment')
  assert.equal(events[0].data, '{"seq":1}')
  assert.equal(events[1].event, 'end')
})

test('ignores unknown events and blank lines', () => {
  const events = parseSseChunk('event: ping\ndata: {}\n\n\n')
  assert.deepEqual(events, [])
})

test('ignores an incomplete trailing event', () => {
  const events = parseSseChunk('event: segment\ndata: {"seq":1}\n\ndata: partial')
  assert.equal(events.length, 1)
})
```

- [ ] **Step 2: Run test to verify it fails**

Tambahkan test fokus dari bagian Review Focus sebelum menjalankan: `openTranscriptStream` dengan `fetch` tiruan yang selalu menjawab 401 harus memanggil `requestToken` satu kali, memakai token baru pada percobaan kedua, dan memanggil `onError` bila percobaan kedua juga 401.

Run: `cd extension && node --test --test-isolation=none src/transcript-stream.test.js`
Expected: FAIL — modul belum ada.

- [ ] **Step 3: Implement the minimal code**

`extension/src/transcript-stream.js`:

```js
export function parseSseChunk(text) {
  const events = []
  const blocks = text.split('\n\n')
  // The trailing block is only complete when the text ends with a blank line.
  if (!text.endsWith('\n\n')) blocks.pop()
  for (const block of blocks) {
    if (!block.trim()) continue
    let name = 'message'
    const data = []
    for (const line of block.split('\n')) {
      if (line.startsWith('event:')) name = line.slice(6).trim()
      else if (line.startsWith('data:')) data.push(line.slice(5).trim())
    }
    if (!data.length) continue
    events.push({ event: name, data: data.join('\n') })
  }
  return events
}
```

Tambahkan fungsi `openTranscriptStream({ url, token, onSegment, onEnd, onError, requestToken })`:

- Memanggil `fetch(url, { headers: { Authorization: `Bearer ${token}` }, cache: 'no-store' })`.
- Bila `response.status === 401`, panggil `requestToken()`; bila token baru ada, ulangi fetch satu kali; bila tetap 401, panggil `onError` dan selesai.
- Bila `!response.ok`, panggil `onError` dan selesai.
- Baca `response.body.getReader()`; kumpulkan teks dan panggil `parseSseChunk` untuk setiap potongan; event `segment` memanggil `onSegment(JSON.parse(data))`, event `end` memanggil `onEnd`, event `error` memanggil `onError`.
- Menutup reader saat selesai.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd extension && node --test --test-isolation=none src/transcript-stream.test.js && npm test`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add extension/src/transcript-stream.js extension/src/transcript-stream.test.js
git commit -m "feat(extension): add SSE parser and transcript stream client"
```

---

### Task 9: State transkrip dan popup

**Files:**
- Create: `extension/src/transcript-state.js`
- Create: `extension/src/transcript-state.test.js`
- Modify: `extension/src/offscreen.js`
- Modify: `extension/src/audio-capture.js`
- Modify: `extension/src/App.jsx`

**Interfaces:**
- Consumes: `parseSseChunk` dan `openTranscriptStream` (Task 8), `toMonoPcm16` (Task 7), `chrome.storage.session`.
- Produces: `transcript-state.js` dengan `emptyState(runId)`, `applySegment(state, segment)`, `applyStatus(state, status)`; kunci storage `transcript_state`; pesan `audio.run.create` yang membawa `sample_rate`.

- [ ] **Step 1: Write the failing test**

Buat `extension/src/transcript-state.test.js`:

```js
import test from 'node:test'
import assert from 'node:assert/strict'
import { emptyState, applySegment } from './transcript-state.js'

test('final replaces the trailing partial and is appended once', () => {
  let state = emptyState('run-1')
  state = applySegment(state, { seq: 1, type: 'partial', text: 'sela' })
  assert.equal(state.partial.text, 'sela')
  state = applySegment(state, { seq: 2, type: 'partial', text: 'selamat' })
  assert.equal(state.partial.text, 'selamat')
  assert.equal(state.finals.length, 0)
  state = applySegment(state, { seq: 3, type: 'final', text: 'selamat pagi' })
  assert.equal(state.partial, null)
  assert.equal(state.finals.length, 1)
  assert.equal(state.finals[0].text, 'selamat pagi')
  assert.equal(state.last_seq, 3)
})

test('finals are capped and oldest entries are dropped', () => {
  let state = emptyState('run-1')
  for (let i = 1; i <= 205; i += 1) state = applySegment(state, { seq: i, type: 'final', text: `baris ${i}` })
  assert.equal(state.finals.length, 200)
  assert.equal(state.finals[0].text, 'baris 6')
})

test('state is scoped to one run', () => {
  const state = emptyState('run-2')
  assert.equal(state.run_id, 'run-2')
  assert.equal(state.finals.length, 0)
  assert.equal(state.partial, null)
})
```

- [ ] **Step 2: Run test to verify it fails**

Tambahkan test fokus dari bagian Review Focus sebelum menjalankan: state yang hanya menerima segmen `partial` harus tetap punya `partial` terisi, `finals` kosong, dan `last_seq` bertambah; `applySegment` tidak boleh melempar.

Run: `cd extension && node --test --test-isolation=none src/transcript-state.test.js`
Expected: FAIL — modul belum ada.

- [ ] **Step 3: Implement the minimal code**

`extension/src/transcript-state.js`:

```js
const FINAL_LIMIT = 200

export function emptyState(runId) {
  return { run_id: runId, status: 'waiting', last_seq: 0, finals: [], partial: null }
}

export function applySegment(state, segment) {
  const next = { ...state, finals: [...state.finals], last_seq: segment.seq }
  if (segment.type === 'final') {
    next.finals.push({ seq: segment.seq, text: segment.text, start: segment.start, duration: segment.duration })
    if (next.finals.length > FINAL_LIMIT) next.finals = next.finals.slice(next.finals.length - FINAL_LIMIT)
    next.partial = null
  } else {
    next.partial = { seq: segment.seq, text: segment.text, start: segment.start, duration: segment.duration }
  }
  next.status = 'transcribing'
  return next
}
```

Sambungkan ke extension:

- `extension/src/audio-capture.js`: urutan start yang dipakai adalah popup → worker `audio.start` (tanpa sample rate); worker memakai `scan()` untuk memilih meeting, memastikan dokumen offscreen ada, lalu mengirim `audio.capture.probe`; dokumen offscreen membuat `AudioContext({ sampleRate: 48000 })`, menjawab `{ sample_rate: context.sampleRate }`, dan menyimpan konteks itu untuk dipakai pada langkah start; worker membuat run dengan `POST /api/audio/runs { meeting_id, sample_rate }` memakai nilai yang dilaporkan, mengambil `stream_id`, lalu mengirim `audio.capture.start` berisi `run_id`, `stream_id`, `token`, `base_url`, dan `sample_rate`. Dokumen offscreen memakai ulang konteks yang sama sehingga sample rate audio dan sample rate yang dikirim ke backend selalu identik.
- `extension/src/offscreen.js`: setelah `audio.capture.start`, buka `openTranscriptStream` dengan URL `${base_url}/api/audio/runs/${run_id}/transcript?since=0`, token dari context, dan `requestToken` yang mengirim pesan `audio.token` ke worker. Simpan state di `chrome.storage.session` kunci `transcript_state` memakai `applySegment`. Tutup stream saat `audio.capture.stop`.
- `extension/src/App.jsx`: baca `transcript_state` dari `chrome.storage.session` dan tampilkan daftar `finals` lalu `partial` terakhir di bawah kartu capture. Tambahkan label status `waiting`, `transcribing`, `transcript_error`, `transcribed`.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd extension && node --test --test-isolation=none src/transcript-state.test.js && npm test && npm run build`
Expected: PASS dan build menghasilkan `dist/offscreen.html`.

- [ ] **Step 5: Commit**

```bash
git add extension/src/transcript-state.js extension/src/transcript-state.test.js extension/src/offscreen.js extension/src/audio-capture.js extension/src/App.jsx
git commit -m "feat(extension): show live transcript segments in the popup"
```

---

### Task 10: Konfigurasi, dokumentasi, dan roadmap

**Files:**
- Modify: `.env`, `.env.example`
- Modify: `README.md`
- Modify: `docs/implementation/audio-status.md`
- Modify: `docs/implementation/feature-roadmap.md`
- Modify: `docs/obsidian-pending/pending.md`

**Interfaces:**
- Consumes: hasil Task 1–9.
- Produces: konfigurasi provider yang bisa dipakai, dokumentasi yang sesuai perilaku aktual.

- [ ] **Step 1: Verify the current state before changing anything**

Run: `grep -E '^(DEEPGRAM_MODEL|DEEPGRAM_LANGUAGE)' .env .env.example`
Expected: `DEEPGRAM_MODEL` kosong atau tidak ada, `DEEPGRAM_LANGUAGE` tidak ada.

- [ ] **Step 2: Apply the configuration**

- `.env`: set `DEEPGRAM_MODEL=nova-2` dan `DEEPGRAM_LANGUAGE=multi`. Jangan sentuh nilai lain yang sudah terisi.
- `.env.example`: tambahkan `DEEPGRAM_MODEL=nova-2` dan `DEEPGRAM_LANGUAGE=multi` sebagai contoh yang bisa diubah.

- [ ] **Step 3: Update documentation**

- `README.md`: tambahkan bagian "Transkripsi live" yang menjelaskan model `nova-2`, bahasa `multi`, format PCM16, event SSE, dan bahwa transkrip tidak disimpan.
- `docs/implementation/audio-status.md`: perbarui bagian format capture menjadi PCM16 `AudioWorklet` dan tambahkan baris verifikasi F4.
- `docs/implementation/feature-roadmap.md`: naikkan status `D-07` menjadi `SELESAI LOKAL` bila test dan build lulus, dan tambahkan fitur F4 (`E-01` sampai `E-07`) dengan status `SELESAI LOKAL` atau `PERLU VALIDASI` sesuai bukti. Tambahkan changelog roadmap.
- `docs/obsidian-pending/pending.md`: tambahkan entri untuk `docs/implementation/audio-status.md` bila belum ada.

- [ ] **Step 4: Verify**

Run: `cd backend && go test ./... && go vet ./... && cd ../extension && npm test && npm run build && cd .. && docker compose config --quiet`
Expected: semuanya lulus.

- [ ] **Step 5: Commit**

```bash
git add .env.example README.md docs/implementation/audio-status.md docs/implementation/feature-roadmap.md docs/obsidian-pending/pending.md
git commit -m "docs: document live transcription setup and verification"
```

Jangan `git add .env`; berkas itu diabaikan Git.

---

### Task 11: Verifikasi akhir dan pemeriksaan provider nyata

**Files:**
- Modify: `fixtures/README.md` bila perlu
- Test: seluruh suite

**Interfaces:**
- Consumes: Task 1–10.
- Produces: bukti bahwa alur backend live bekerja dan batas verifikasi tercatat.

- [ ] **Step 1: Run the full local verification**

Run:
```bash
cd backend && go test -race -count=1 ./... && go vet ./... && cd ../extension && npm test && npm run build && cd .. && node --test --test-isolation=none http/auth.test.mjs && docker compose config --quiet && sh migration/tests/check-meetings.sh
```
Expected: semua lulus, `go test` melaporkan 0 fail.

- [ ] **Step 2: Start the real backend and verify health**

Run: `docker compose up -d --build backend` lalu `curl --fail http://127.0.0.1:8989/health`
Expected: `{"service":"livetranslate-backend","status":"ok"}`.

- [ ] **Step 3: Verify the provider check with the configured model**

Jalankan pemeriksaan provider memakai berkas audio contoh dari cache SDK (bukan data pengguna) karena `fixtures/check.wav` milik pengguna belum ada:

```bash
set -a; . ./.env; set +a
docker compose run --rm backend -check deepgram -audio /app/fixtures/check.wav
```
Karena fixture milik pengguna belum ada, salin berkas contoh SDK ke `fixtures/check.wav` terlebih dahulu (berkas ini diabaikan Git) lalu jalankan perintah di atas.

Expected: `deepgram: LULUS`. Bila gagal, catat pesan error apa adanya; jangan mengarang klaim.

- [ ] **Step 4: Verify the live capture endpoints against the real backend**

Run:
```bash
TOKEN=$(curl -s -X POST http://127.0.0.1:8989/api/auth/login -H 'Content-Type: application/json' -d '{"email":"livetranslate.e2e@livetranslate.invalid","password":"LtE2e-2026!test"}' | jq -r '.session.access_token')
curl -s -o /tmp/run.json -w 'HTTP %{http_code}\n' -X POST http://127.0.0.1:8989/api/audio/runs -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"meeting_id":"<meeting-id>","sample_rate":48000}'
```
Expected: 201 dengan run yang aktif. Bila 502 `deepgram_unreachable`, periksa log container dan laporkan apa adanya.

- [ ] **Step 5: Record the verification result**

Catat hasil di `docs/implementation/audio-status.md`: tabel verifikasi diisi dengan hasil nyata, termasuk bagian yang tetap BELUM DIUJI (Chrome nyata, streaming end-to-end dari browser).

- [ ] **Step 6: Commit**

```bash
git add docs/implementation/audio-status.md
git commit -m "docs: record live transcription verification results"
```

---

## Definition of Done

- Semua test Go, test JavaScript, `go vet`, build extension, contract test, `docker compose config`, dan harness migrasi lulus.
- Tidak ada modul Go atau paket npm baru.
- Tidak ada secret di source, build artifact, atau log.
- Audio dan transkrip tidak tertulis di database, log, atau file.
- Roadmap dan status dokumen mencerminkan bukti yang benar-benar dijalankan, termasuk bagian yang BELUM DIUJI.
- Chrome nyata tetap tercatat sebagai BELUM DIUJI bila tidak ada binary Chrome di lingkungan.
