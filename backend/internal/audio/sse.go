package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"livetranslate/backend/internal/transcribe"
	"net/http"
	"strconv"
	"time"
)

// transcriptBudget bounds the identification and the ownership read of the
// transcript route. The stream itself runs on the context of the request, so it
// lasts as long as the client keeps the connection open.
var transcriptBudget = 5 * time.Second

// transcript streams live in-memory segments as Server-Sent Events. Durable
// opt-in finals are written separately and read through the archive API.
// The phase before the stream has its own budget, and the stream itself runs
// on the context of the request, so it lasts as long as the client keeps the
// connection open.
func (h *handler) transcript(w http.ResponseWriter, r *http.Request, runID string) {
	// The identification and the ownership read share one budget, so a slow
	// database cannot hold the route without a deadline. The stream itself
	// keeps the context of the request, so it ends when the client leaves.
	ctx, cancel := context.WithTimeout(r.Context(), transcriptBudget)
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
	// account is hidden by RLS: the stream of another account is never opened.
	run, found, err := h.repo.get(ctx, runID, user, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	if run.Status == "stopped" {
		// The run is over, so the client gets the end event right away instead
		// of a stream that stays open without a transcript.
		writeStreamHeader(w)
		_ = writeEvent(w, "end", map[string]string{"run_id": runID})
		return
	}
	session, live := h.broker.Session(runID)
	if !live {
		writeFailure(w, &failure{503, "transcript_unavailable", "Transkripsi tidak tersedia."})
		return
	}
	writeStreamHeader(w)
	h.streamSegments(w, r, session, runID)
}

// streamSegments writes every segment of the session as an event, until the
// session ends or the client disconnects.
func (h *handler) streamSegments(w http.ResponseWriter, r *http.Request, session *transcribe.Session, runID string) {
	segments, release := session.Subscribe(parseSince(r))
	// The subscription ends with the request, so a client that leaves does not
	// hold a place in the session.
	defer release()
	for {
		select {
		case <-r.Context().Done():
			return
		case segment, ok := <-segments:
			if !ok {
				// The channel closes on a normal end and on a provider failure,
				// so the terminal error of the session tells them apart.
				if session.Err() != nil {
					_ = writeEvent(w, "error", map[string]string{"code": "transcript_error", "message": "Transkripsi gagal."})
					return
				}
				_ = writeEvent(w, "end", map[string]string{"run_id": runID})
				return
			}
			if err := writeEvent(w, "segment", segment); err != nil {
				return
			}
		}
	}
}

// parseSince reads the sequence number the client already holds. A value that
// cannot be read means the whole backlog, so the client never misses a
// segment.
func parseSince(r *http.Request) int {
	since, err := strconv.Atoi(r.URL.Query().Get("since"))
	if err != nil || since < 0 {
		return 0
	}
	return since
}

// writeStreamHeader prepares the response for the event stream. The write
// deadline of the server is cleared, so the stream is not cut off by the
// write timeout of the server.
func writeStreamHeader(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// writeEvent writes one event of the stream. The payload is JSON, so the
// extension reads it without parsing a text format.
func writeEvent(w http.ResponseWriter, name string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
		return err
	}
	// The event leaves the server right away, so the extension sees the
	// transcript while the run is still live.
	_ = http.NewResponseController(w).Flush()
	return nil
}
