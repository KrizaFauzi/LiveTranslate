package audio

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"livetranslate/backend/internal/assistance"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/sumopod"
	"livetranslate/backend/internal/transcribe"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// maxChunkBytes bounds one uploaded audio chunk.
	maxChunkBytes = 2 << 20
	// maxRunBytes bounds the total streamed volume of one run.
	maxRunBytes    = 1 << 30
	maxRunDuration = 4 * time.Hour
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
	// Session.Close has a worst case of about 20 seconds (provider finalize
	// 5 s, flush window 10 s, teardown 5 s), so the close gets its own budget
	// on a context detached from the request deadline: a request-scoped
	// deadline would cancel the close halfway and the final segment would be lost.
	// A close that needs longer than the budget finishes in the background, and
	// the final segment still reaches the subscriber of the open stream.
	closeBudget = 10 * time.Second
	// bookkeepingBudget bounds the run bookkeeping of the stop route. It runs
	// while the session closes, so the two budgets do not add up.
	bookkeepingBudget = 10 * time.Second
	// stopMetadataBudget keeps the final archive metadata read inside the
	// server write window: 5 s ownership + 10 s close/bookkeeping + 1 s read.
	stopMetadataBudget = time.Second
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
	repo       repository
	broker     *transcribe.Broker
	verify     func(context.Context, string) (string, *auth.VerificationError)
	translator *sumopod.Client
	config     config.Config
	client     *http.Client
	// owners remembers the user and the token of every open run, so a run whose
	// provider died can be closed without the client asking for it.
	mu      sync.Mutex
	owners  map[string]runOwner
	tickets atomic.Uint64
	now     func() time.Time
}

// runOwner is the account that started a run, with the token that proves it.
type runOwner struct {
	user           string
	token          string
	ticket         uint64
	writer         *transcriptWriter
	translator     *translationWorker
	assistant      *assistance.Worker
	cleanupStarted bool
}

// NewHandler serves the capture-run API. The uploaded audio is forwarded to the
// transcription session of the run and is never stored.
func NewHandler(c config.Config, client *http.Client, broker *transcribe.Broker) http.Handler {
	if broker == nil {
		broker = transcribe.NewBroker(c, nil)
	}
	h := &handler{repo: newRepository(c, client), broker: broker, translator: sumopod.NewClient(c, client), config: c, client: client, verify: auth.NewUserVerifier(c, client), owners: map[string]runOwner{}, now: time.Now}
	// A session whose provider died leaves a run that cannot continue: the run
	// is closed here, so it does not stay active forever.
	broker.OnProviderEnd(h.markRunFailed)
	return h
}

// rememberOwner seeds storage before provider supervision can publish a final.
func (h *handler) rememberOwner(run Run, token string, ticket uint64) *transcriptWriter {
	var writer *transcriptWriter
	var translator *translationWorker
	var assistant *assistance.Worker
	if run.SaveTranscript {
		translator = newTranslationWorker(h.repo, run, token, h.translator)
		assistant = assistance.New(h.config, h.client, assistance.Run{ID: run.ID, UserID: run.UserID, MeetingID: run.MeetingID, OccurrenceID: run.OccurrenceID}, token)
		writer = newTranscriptWriterWithCallback(h.repo, run, token, ticket, func(segment transcribe.Segment) {
			translator.enqueue(segment)
			assistant.Enqueue(segment)
		})
	}
	h.mu.Lock()
	h.owners[run.ID] = runOwner{user: run.UserID, token: token, ticket: ticket, writer: writer, translator: translator, assistant: assistant}
	h.mu.Unlock()
	return writer
}

func (h *handler) refreshOwner(runID, user, token string, ticket uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	owner, ok := h.owners[runID]
	if !ok || owner.user != user || ticket <= owner.ticket {
		return
	}
	owner.token = token
	owner.ticket = ticket
	if owner.writer != nil {
		owner.writer.refresh(user, token, ticket)
	}
	if owner.translator != nil {
		owner.translator.refresh(user, token)
	}
	if owner.assistant != nil {
		owner.assistant.Refresh(user, token)
	}
	h.owners[runID] = owner
}

// releaseOwner retains verified credentials until finalization and storage drain
// have ended, bounded by the same 30-second budget used by the writer.
func (h *handler) releaseOwner(runID string, session *transcribe.Session, closed <-chan struct{}) {
	h.mu.Lock()
	owner, ok := h.owners[runID]
	if !ok || owner.cleanupStarted {
		h.mu.Unlock()
		return
	}
	owner.cleanupStarted = true
	h.owners[runID] = owner
	h.mu.Unlock()
	go func() {
		timer := time.NewTimer(transcriptDrainBudget)
		defer timer.Stop()
		if closed != nil {
			select {
			case <-closed:
			case <-timer.C:
				if session != nil {
					session.Abort()
				}
				if owner.writer != nil {
					owner.writer.mu.Lock()
					owner.writer.failLocked()
					owner.writer.mu.Unlock()
				}
				h.dropOwner(runID)
				return
			}
		}
		if owner.writer != nil {
			select {
			case <-owner.writer.done:
			case <-timer.C:
				owner.writer.mu.Lock()
				owner.writer.failLocked()
				owner.writer.mu.Unlock()
			}
		}
		if owner.translator != nil {
			owner.translator.close()
		}
		if owner.assistant != nil {
			owner.assistant.Close()
		}
		h.dropOwner(runID)
	}()
}

func (h *handler) dropOwner(runID string) {
	h.mu.Lock()
	delete(h.owners, runID)
	h.mu.Unlock()
}

func (h *handler) markRunFailed(runID string) {
	h.mu.Lock()
	owner, ok := h.owners[runID]
	h.mu.Unlock()
	if !ok {
		return
	}
	h.failRun(runID, owner.user, owner.token)
	// The broker invokes this only after actual session termination; the
	// session has already finished its final sink.
	h.releaseOwner(runID, nil, nil)
}

func (h *handler) failRun(runID, user, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), failureBudget)
	defer cancel()
	_ = h.repo.markFailed(ctx, runID, user, token)
}

// ServeHTTP routes the capture API. Every route builds its own context, so the
// transcript route can stream without the deadline of the other routes.
func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	runPath := strings.TrimPrefix(path, "/api/audio/runs/")
	switch {
	case path == "/api/audio/runs" && r.Method == "POST":
		h.start(w, r)
	case path == "/api/audio/runs" && r.Method == "GET":
		h.listArchives(w, r)
	case strings.HasPrefix(path, "/api/audio/runs/") && strings.HasSuffix(runPath, "/translations") && r.Method == "GET":
		h.readTranslations(w, r, strings.TrimSuffix(runPath, "/translations"))
	case strings.HasPrefix(path, "/api/audio/runs/") && strings.HasSuffix(runPath, "/segments") && r.Method == "GET":
		h.readArchive(w, r, strings.TrimSuffix(runPath, "/segments"))
	case strings.HasPrefix(path, "/api/audio/runs/") && strings.HasSuffix(runPath, "/segments") && r.Method == "DELETE":
		h.deleteArchive(w, r, strings.TrimSuffix(runPath, "/segments"))
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
	ticket := h.tickets.Add(1)
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
	meetingID, occurrenceID, sampleRate, valid := parseRunRequest(data)
	if !valid {
		writeFailure(w, &failure{400, "invalid_input", "Kirim meeting_id, occurrence_id, dan sample_rate yang valid."})
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
	run, err := h.repo.create(ctx, meetingID, occurrenceID, user, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	writer := h.rememberOwner(run, token, ticket)
	var sink transcribe.FinalSink
	if writer != nil {
		sink = writer
	}
	if _, openErr := h.broker.Open(ctx, run.ID, sampleRate, sink); openErr != nil {
		if writer != nil {
			writer.mu.Lock()
			writer.failLocked()
			writer.mu.Unlock()
			writer.Finish()
		}
		h.releaseOwner(run.ID, nil, nil)
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
	run = h.withStorage(run)
	writeJSON(w, 201, map[string]any{"run": run, "storage": run.Storage})
}

// parseRunRequest reads the body of a run creation. The sample rate is required
// because the provider needs it to decode the PCM16 stream.
func parseRunRequest(data []byte) (string, string, int, bool) {
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) != nil || len(raw) != 3 {
		return "", "", 0, false
	}
	for key := range raw {
		if key != "meeting_id" && key != "occurrence_id" && key != "sample_rate" {
			return "", "", 0, false
		}
	}
	var meetingID, occurrenceID string
	if json.Unmarshal(raw["meeting_id"], &meetingID) != nil || !uuidPattern.MatchString(meetingID) {
		return "", "", 0, false
	}
	if json.Unmarshal(raw["occurrence_id"], &occurrenceID) != nil || !uuidPattern.MatchString(occurrenceID) {
		return "", "", 0, false
	}
	var rate int
	if json.Unmarshal(raw["sample_rate"], &rate) != nil || rate <= 0 {
		return "", "", 0, false
	}
	return meetingID, occurrenceID, rate, true
}

// chunk forwards the uploaded audio to the transcription session of the run and
// accounts the bytes. The audio itself is never stored.
func (h *handler) chunk(w http.ResponseWriter, r *http.Request, runID string) {
	ticket := h.tickets.Add(1)
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
	h.refreshOwner(runID, user, token, ticket)
	if run.Status != "active" {
		writeFailure(w, &failure{409, "run_closed", "Run capture sudah ditutup."})
		return
	}
	startedAt, parseErr := time.Parse(time.RFC3339Nano, run.StartedAt)
	if parseErr != nil || !startedAt.Add(maxRunDuration).After(h.now()) {
		h.failRun(runID, user, token)
		writeFailure(w, &failure{409, "occurrence_limit", "Batas durasi meeting empat jam tercapai."})
		return
	}
	if run.BytesReceived+int64(len(payload)) > maxRunBytes {
		writeFailure(w, &failure{413, "body_limit", "Ukuran run capture melebihi batas."})
		return
	}
	session, live := h.broker.Session(runID)
	if !live {
		// The session is gone: the run cannot continue, so it is closed here
		// instead of staying active forever.
		h.failRun(runID, user, token)
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
	updated = h.withStorage(updated)
	writeJSON(w, 200, map[string]any{"run": updated, "storage": updated.Storage})
}

// stop closes the transcription session of the run, marks the run stopped, and
// drops the session from the broker.
func (h *handler) stop(w http.ResponseWriter, r *http.Request, runID string) {
	ticket := h.tickets.Add(1)
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
	h.refreshOwner(runID, user, token, ticket)
	h.mu.Lock()
	owner, owned := h.owners[runID]
	h.mu.Unlock()
	if owned && owner.writer != nil {
		owner.writer.beginDrain()
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
	h.releaseOwner(runID, session, closed)
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
	// Capture bookkeeping can return before the writer acknowledges its last
	// final. Refresh that row without depending on the live owner map surviving
	// cleanup, while preserving terminal delete/expiry returned by the DB.
	metadataUnavailable := false
	if run.SaveTranscript && run.TranscriptState == "saving" {
		metadataCtx, cancelMetadata := context.WithTimeout(context.WithoutCancel(r.Context()), stopMetadataBudget)
		latest, exists, metadataErr := h.repo.get(metadataCtx, runID, user, token)
		cancelMetadata()
		if metadataErr == nil && exists {
			run = latest
		} else {
			// Capture stop was already acknowledged. Archive metadata failure
			// must not turn it into an error and bypass the client's final wait.
			metadataUnavailable = true
		}
	}
	metadata := h.storageWithWriter(run, owner.writer)
	if metadataUnavailable && metadata.Complete {
		metadata.State = "incomplete"
		metadata.Complete = false
	}
	run.Storage = &metadata
	writeJSON(w, 200, map[string]any{"run": run, "storage": metadata})
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
