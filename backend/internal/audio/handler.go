package audio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/transcribe"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	// maxChunkBytes bounds one uploaded audio chunk.
	maxChunkBytes = 2 << 20
	// maxRunBytes bounds the total streamed volume of one run.
	maxRunBytes = 1 << 30
)

// The budgets of the capture routes. The server writes the response within
// 2 * config.ConnectionTimeout, so every route has to answer before that, and
// the phases of a route add up to less than it.
var (
	// startBudget bounds the identification, the ownership read, and the run row
	// of the run creation.
	startBudget = 5 * time.Second
	// failureBudget bounds the bookkeeping of a run whose session did not open.
	failureBudget = 3 * time.Second
	// ownershipBudget bounds the identification and the ownership read of the stop
	// route.
	ownershipBudget = 5 * time.Second
	// closeBudget bounds the wait for the session close of the stop route.
	// Session.Close has a worst case of about 10.5 seconds (provider finalize
	// 5 s, flush window 0.5 s, teardown 5 s), so the close gets its own budget
	// on a context detached from the request deadline: a request-scoped
	// deadline would cancel the close halfway and the final segment would be lost.
	closeBudget = 10 * time.Second
	// bookkeepingBudget bounds the run bookkeeping of the stop route. It runs
	// while the session closes, so the two budgets do not add up.
	bookkeepingBudget = 10 * time.Second
)

var uuidPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeFailure(w http.ResponseWriter, e *failure) {
	writeJSON(w, e.status, map[string]any{"error": map[string]string{"code": e.code, "message": e.message}})
}

// handler serves the capture-run API. It holds the run repository, the
// transcription broker, and the token verifier shared by every route.
type handler struct {
	repo   repository
	broker *transcribe.Broker
	verify func(context.Context, string) (string, *auth.VerificationError)
}

// NewHandler serves the capture-run API. The uploaded audio is forwarded to the
// transcription session of the run and is never stored.
func NewHandler(c config.Config, client *http.Client, broker *transcribe.Broker) http.Handler {
	if broker == nil {
		broker = transcribe.NewBroker(c, nil)
	}
	return &handler{repo: newRepository(c, client), broker: broker, verify: auth.NewUserVerifier(c, client)}
}

// ServeHTTP routes the capture API. Every route builds its own context, so the
// transcript route can stream without the deadline of the other routes.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	runPath := strings.TrimPrefix(path, "/api/audio/runs/")
	switch {
	case path == "/api/audio/runs" && r.Method == "POST":
		h.start(w, r)
	case strings.HasSuffix(runPath, "/chunks") && r.Method == "POST":
		h.chunk(w, r, strings.TrimSuffix(runPath, "/chunks"))
	case strings.HasSuffix(runPath, "/stop") && r.Method == "POST":
		h.stop(w, r, strings.TrimSuffix(runPath, "/stop"))
	case strings.HasSuffix(runPath, "/transcript") && r.Method == "GET":
		h.transcript(w, r, strings.TrimSuffix(runPath, "/transcript"))
	default:
		writeFailure(w, &failure{404, "not_found", "Endpoint tidak tersedia."})
	}
}

func bearer(r *http.Request) (string, *failure) {
	fields := strings.Fields(r.Header.Get("Authorization"))
	if len(fields) != 2 || !strings.EqualFold(fields[0], "Bearer") || len(fields[1]) > 12000 {
		return "", &failure{401, "unauthorized", "Sesi tidak valid. Silakan masuk kembali."}
	}
	return fields[1], nil
}

func identify(ctx context.Context, verify func(context.Context, string) (string, *auth.VerificationError), r *http.Request) (string, string, *failure) {
	token, err := bearer(r)
	if err != nil {
		return "", "", err
	}
	user, verifyErr := verify(ctx, token)
	if verifyErr != nil {
		return "", "", &failure{verifyErr.Status, verifyErr.Code, verifyErr.Message}
	}
	return user, token, nil
}

// start creates the run row and opens the transcription session of the run. The
// row comes first, so a session that cannot be opened leaves a run marked failed.
func (h *handler) start(w http.ResponseWriter, r *http.Request) {
	// The identification, the ownership read, and the run row share one budget,
	// so the route reaches the handshake of the provider in time. The handshake
	// itself has its own budget inside the broker.
	ctx, cancel := context.WithTimeout(r.Context(), startBudget)
	defer cancel()
	user, token, err := identify(ctx, h.verify, r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	content, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || content != "application/json" {
		writeFailure(w, &failure{415, "content_type", "Gunakan JSON."})
		return
	}
	data, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 16384))
	if readErr != nil {
		var limit *http.MaxBytesError
		if errors.As(readErr, &limit) {
			writeFailure(w, &failure{413, "body_limit", "Permintaan terlalu besar."})
			return
		}
		writeFailure(w, &failure{400, "invalid_input", "Kirim meeting_id dan sample_rate yang valid."})
		return
	}
	meetingID, sampleRate, valid := parseRunRequest(data)
	if !valid {
		writeFailure(w, &failure{400, "invalid_input", "Kirim meeting_id dan sample_rate yang valid."})
		return
	}
	owned, err := h.repo.meetingOwned(ctx, meetingID, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !owned {
		writeFailure(w, &failure{404, "not_found", "Sesi meeting tidak ditemukan."})
		return
	}
	if !h.broker.Configured() {
		writeFailure(w, &failure{503, "deepgram_config", "Konfigurasi transkripsi belum lengkap."})
		return
	}
	run, err := h.repo.create(ctx, meetingID, user, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if _, openErr := h.broker.Open(ctx, run.ID, sampleRate); openErr != nil {
		// The failed run is marked on a context of its own, so a provider that
		// used up the whole request budget still leaves the run marked failed.
		failedCtx, cancelFailed := context.WithTimeout(context.WithoutCancel(r.Context()), failureBudget)
		defer cancelFailed()
		_ = h.repo.markFailed(failedCtx, run.ID, user, token)
		if errors.Is(openErr, transcribe.ErrProviderConfig) {
			writeFailure(w, &failure{503, "deepgram_config", "Konfigurasi transkripsi belum lengkap."})
			return
		}
		writeFailure(w, &failure{502, "deepgram_unreachable", "Layanan transkripsi tidak dapat dijangkau."})
		return
	}
	writeJSON(w, 201, map[string]any{"run": run})
}

// parseRunRequest reads the body of a run creation. The sample rate is required
// because the provider needs it to decode the PCM16 stream.
func parseRunRequest(data []byte) (string, int, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) != 2 {
		return "", 0, false
	}
	var meetingID string
	value, ok := raw["meeting_id"]
	if !ok || json.Unmarshal(value, &meetingID) != nil || !uuidPattern.MatchString(meetingID) {
		return "", 0, false
	}
	var sampleRate int
	rate, ok := raw["sample_rate"]
	if !ok || json.Unmarshal(rate, &sampleRate) != nil || sampleRate <= 0 {
		return "", 0, false
	}
	return meetingID, sampleRate, true
}

// chunk forwards the uploaded audio to the transcription session of the run and
// accounts the bytes. The audio itself is never stored.
func (h *handler) chunk(w http.ResponseWriter, r *http.Request, runID string) {
	ctx, cancel := context.WithTimeout(r.Context(), config.ConnectionTimeout)
	defer cancel()
	user, token, err := identify(ctx, h.verify, r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !uuidPattern.MatchString(runID) {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	payload, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChunkBytes+1))
	if readErr != nil {
		var limit *http.MaxBytesError
		if errors.As(readErr, &limit) {
			writeFailure(w, &failure{413, "body_limit", "Chunk audio terlalu besar."})
			return
		}
		writeFailure(w, &failure{400, "invalid_input", "Chunk audio tidak valid."})
		return
	}
	if len(payload) == 0 {
		writeFailure(w, &failure{400, "invalid_input", "Chunk audio kosong."})
		return
	}
	if len(payload) > maxChunkBytes {
		writeFailure(w, &failure{413, "body_limit", "Chunk audio terlalu besar."})
		return
	}
	run, found, err := h.repo.get(ctx, runID, user, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	if run.Status != "active" {
		writeFailure(w, &failure{409, "run_closed", "Run capture sudah ditutup."})
		return
	}
	if run.BytesReceived+int64(len(payload)) > maxRunBytes {
		writeFailure(w, &failure{413, "body_limit", "Ukuran run capture melebihi batas."})
		return
	}
	session, live := h.broker.Session(runID)
	if !live {
		writeFailure(w, &failure{409, "run_closed", "Run capture sudah ditutup."})
		return
	}
	if writeErr := session.Write(payload); writeErr != nil {
		writeFailure(w, &failure{502, "deepgram_unreachable", "Layanan transkripsi tidak dapat dijangkau."})
		return
	}
	updated, found, err := h.repo.appendChunk(ctx, runID, len(payload), user, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeFailure(w, &failure{409, "run_closed", "Run capture sudah ditutup."})
		return
	}
	writeJSON(w, 200, map[string]any{"run": updated})
}

// stop closes the transcription session of the run, marks the run stopped, and
// drops the session from the broker.
func (h *handler) stop(w http.ResponseWriter, r *http.Request, runID string) {
	// The identification and the ownership read share one budget, so the close
	// phase starts at the latest after that budget.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), ownershipBudget)
	defer cancel()
	user, token, err := identify(ctx, h.verify, r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !uuidPattern.MatchString(runID) {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	// The run is read with the token of the caller, so the row of another
	// account is hidden by RLS: the session of another account is never closed.
	// A run whose row stays invisible keeps its session, because the caller
	// cannot prove the ownership of that run.
	if _, found, err := h.repo.get(ctx, runID, user, token); err != nil {
		writeFailure(w, err)
		return
	} else if !found {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	session, live := h.broker.Session(runID)
	// The close runs on a context detached from the request deadline, because it
	// can take longer than the request budget and the final segment must survive.
	closeCtx, cancelClose := context.WithTimeout(context.WithoutCancel(r.Context()), closeBudget)
	defer cancelClose()
	// The bookkeeping gets its own detached budget and runs while the session
	// closes, so the route waits for both without exceeding the write timeout
	// of the server.
	bookCtx, cancelBook := context.WithTimeout(context.WithoutCancel(r.Context()), bookkeepingBudget)
	defer cancelBook()
	closed := closeSession(session)
	run, found, err := h.repo.stopRun(bookCtx, runID, user, token)
	select {
	case <-closed:
	case <-closeCtx.Done():
	}
	if live {
		// The session is closed, so it leaves the broker whatever the
		// bookkeeping answered: a dead session must not stay behind.
		h.broker.Remove(runID)
	}
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	writeJSON(w, 200, map[string]any{"run": run})
}

// closeSession ends the transcription session of one run in the background. The
// returned channel is closed when the close finished. A run without a live
// session closes the channel at once.
func closeSession(session *transcribe.Session) <-chan struct{} {
	closed := make(chan struct{})
	if session == nil {
		close(closed)
		return closed
	}
	go func() {
		defer close(closed)
		_ = session.Close()
	}()
	return closed
}

// RegisterRoutes registers all routes owned by the capture feature.
func RegisterRoutes(mux *http.ServeMux, c config.Config, broker *transcribe.Broker) {
	mux.Handle("/api/audio/", NewHandler(c, nil, broker))
}
