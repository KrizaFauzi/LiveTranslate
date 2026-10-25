package audio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"livetranslate/backend/internal/transcribe"
)

type storageMetadata struct {
	State     string            `json:"state"`
	ExpiresAt nullableTimestamp `json:"expires_at"`
	Complete  bool              `json:"complete"`
}

type archiveCursor struct {
	User    string `json:"user"`
	Meeting string `json:"meeting"`
	Started string `json:"started"`
	ID      string `json:"id"`
}

func (h *handler) storage(run Run) storageMetadata {
	return h.storageWithWriter(run, nil)
}

// A stop response may retain its verified controller after map cleanup. That
// controller's acknowledged terminal status must not become "incomplete".
func (h *handler) storageWithWriter(run Run, writer *transcriptWriter) storageMetadata {
	state := run.TranscriptState
	if !run.SaveTranscript && state != "deleted" {
		state = "disabled"
	}
	if state == "" {
		state = "disabled"
	}
	if state != "deleted" && run.SaveTranscript {
		if expiry, err := time.Parse(time.RFC3339Nano, string(run.TranscriptExpiresAt)); err == nil && !expiry.After(h.now()) {
			state = "expired"
		}
	}
	if state == "saving" || state == "saved" {
		if writer == nil {
			h.mu.Lock()
			owner := h.owners[run.ID]
			h.mu.Unlock()
			writer = owner.writer
		}
		if writer != nil {
			state = writer.state()
		} else if state == "saving" {
			state = "incomplete"
		}
	}
	return storageMetadata{State: state, ExpiresAt: run.TranscriptExpiresAt, Complete: state == "saved"}
}

func (h *handler) withStorage(run Run) Run {
	metadata := h.storage(run)
	run.Storage = &metadata
	return run
}

func invalidPagination() *failure {
	return &failure{400, "invalid_pagination", "Paginasi transkrip tidak valid."}
}
func archiveNotFound() *failure {
	return &failure{404, "not_found", "Arsip transkrip tidak ditemukan."}
}

func queryInteger(r *http.Request, name string, fallback, min, max int) (int, bool) {
	values, present := r.URL.Query()[name]
	if !present {
		return fallback, true
	}
	if len(values) != 1 || values[0] == "" {
		return 0, false
	}
	for _, char := range values[0] {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(values[0])
	return n, err == nil && n >= min && n <= max
}

func (h *handler) archiveOwner(ctx context.Context, r *http.Request, id string) (Run, string, *failure) {
	user, token, err := identify(ctx, h.verify, r)
	if err != nil {
		return Run{}, "", err
	}
	if !uuidPattern.MatchString(id) {
		return Run{}, "", archiveNotFound()
	}
	run, found, err := h.repo.get(ctx, id, user, token)
	if err != nil {
		return Run{}, "", err
	}
	if !found {
		return Run{}, "", archiveNotFound()
	}
	if values, present := r.URL.Query()["meeting_id"]; present && (len(values) != 1 || !uuidPattern.MatchString(values[0]) || values[0] != run.MeetingID) {
		return Run{}, "", archiveNotFound()
	}
	return run, token, nil
}

func (h *handler) listArchives(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), transcriptRequestBudget)
	defer cancel()
	user, token, err := identify(ctx, h.verify, r)
	if err != nil {
		writeFailure(w, err)
		return
	}
	meetingID := r.URL.Query().Get("meeting_id")
	if !uuidPattern.MatchString(meetingID) {
		writeFailure(w, archiveNotFound())
		return
	}
	limit, valid := queryInteger(r, "limit", 20, 1, 100)
	if !valid {
		writeFailure(w, invalidPagination())
		return
	}
	var cursor *archiveCursor
	if values, present := r.URL.Query()["cursor"]; present {
		if len(values) != 1 || len(values[0]) > 2048 {
			writeFailure(w, invalidPagination())
			return
		}
		data, decodeErr := base64.RawURLEncoding.DecodeString(values[0])
		var parsed archiveCursor
		if decodeErr != nil || json.Unmarshal(data, &parsed) != nil || parsed.User != user || parsed.Meeting != meetingID || !uuidPattern.MatchString(parsed.ID) {
			writeFailure(w, invalidPagination())
			return
		}
		started, parseErr := time.Parse(time.RFC3339Nano, parsed.Started)
		if parseErr != nil {
			writeFailure(w, invalidPagination())
			return
		}
		parsed.Started = started.UTC().Format(time.RFC3339Nano)
		cursor = &parsed
	}
	owned, err := h.repo.meetingOwned(ctx, meetingID, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !owned {
		writeFailure(w, archiveNotFound())
		return
	}
	runs, err := h.repo.listTranscripts(ctx, user, meetingID, token, limit+1, cursor, h.now())
	if err != nil {
		writeFailure(w, err)
		return
	}
	var next *string
	if len(runs) > limit {
		runs = runs[:limit]
		last := runs[len(runs)-1]
		encoded, _ := json.Marshal(archiveCursor{User: user, Meeting: meetingID, Started: last.StartedAt, ID: last.ID})
		value := base64.RawURLEncoding.EncodeToString(encoded)
		next = &value
	}
	if runs == nil {
		runs = []Run{}
	}
	for i := range runs {
		runs[i] = h.withStorage(runs[i])
	}
	writeJSON(w, 200, map[string]any{"runs": runs, "next_cursor": next})
}

func (h *handler) readArchive(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), transcriptRequestBudget)
	defer cancel()
	run, token, err := h.archiveOwner(ctx, r, id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	after, validAfter := queryInteger(r, "after_seq", 0, 0, 2147483647)
	through, validThrough := queryInteger(r, "through_seq", 0, 0, 2147483647)
	limit, validLimit := queryInteger(r, "limit", 50, 1, 100)
	_, snapshotSet := r.URL.Query()["through_seq"]
	if !validAfter || !validThrough || !validLimit || snapshotSet && after > through {
		writeFailure(w, invalidPagination())
		return
	}
	metadata := h.storage(run)
	snapshotComplete := metadata.Complete
	segments := []transcribe.Segment{}
	var next *int
	if metadata.State != "disabled" && metadata.State != "deleted" && metadata.State != "expired" {
		if !snapshotSet {
			var readErr *failure
			through, readErr = h.repo.maxTranscriptSeq(ctx, run, token)
			if readErr != nil {
				writeFailure(w, readErr)
				return
			}
		}
		if through > after {
			rows, readErr := h.repo.segmentPage(ctx, run, token, after, through, limit+1)
			if readErr != nil {
				writeFailure(w, readErr)
				return
			}
			if len(rows) > limit {
				rows = rows[:limit]
				value := rows[len(rows)-1].Seq
				next = &value
			}
			for _, row := range rows {
				segments = append(segments, transcribe.Segment{Seq: row.Seq, Type: "final", Text: row.Text, Start: row.Start, Duration: row.Duration, Language: row.Language})
			}
		}
		// Explicit ceilings must cover the completed archive, including zero.
		// Check before the final metadata read: deletion/expiry can hide all
		// rows from the max query and must not turn an old ceiling complete.
		ceilingComplete := true
		if snapshotComplete && snapshotSet {
			maximum, maxErr := h.repo.maxTranscriptSeq(ctx, run, token)
			if maxErr != nil {
				writeFailure(w, maxErr)
				return
			}
			ceilingComplete = through >= maximum
		}
		// A delete/expiry that completed during a page read hides its text too.
		latest, found, readErr := h.repo.get(ctx, id, run.UserID, token)
		if readErr != nil {
			writeFailure(w, readErr)
			return
		}
		if !found {
			writeFailure(w, archiveNotFound())
			return
		}
		run = latest
		metadata = h.storage(run)
		// A later successful drain cannot promote an earlier active snapshot.
		metadata.Complete = metadata.Complete && snapshotComplete && ceilingComplete
		if metadata.State == "disabled" || metadata.State == "deleted" || metadata.State == "expired" {
			segments = []transcribe.Segment{}
			next = nil
		}
	}
	run.Storage = &metadata
	writeJSON(w, 200, map[string]any{"run": run, "storage": metadata, "segments": segments, "through_seq": through, "next_after_seq": next})
}

func (h *handler) deleteArchive(w http.ResponseWriter, r *http.Request, id string) {
	ctx, cancel := context.WithTimeout(r.Context(), transcriptRequestBudget)
	defer cancel()
	run, token, err := h.archiveOwner(ctx, r, id)
	if err != nil {
		writeFailure(w, err)
		return
	}
	updated, found, err := h.repo.deleteTranscript(ctx, run, token)
	if err != nil {
		writeFailure(w, err)
		return
	}
	if !found {
		writeFailure(w, archiveNotFound())
		return
	}
	h.mu.Lock()
	owner, live := h.owners[id]
	h.mu.Unlock()
	if live && owner.writer != nil {
		owner.writer.terminate("deleted")
	}
	updated = h.withStorage(updated)
	writeJSON(w, 200, map[string]any{"run": updated, "storage": updated.Storage})
}
