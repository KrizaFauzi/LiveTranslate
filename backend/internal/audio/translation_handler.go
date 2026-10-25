package audio

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

func (h *handler) readTranslations(w http.ResponseWriter, r *http.Request, runID string) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
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
	run, found, repoErr := h.repo.get(ctx, runID, user, token)
	if repoErr != nil {
		writeFailure(w, repoErr)
		return
	}
	if !found {
		writeFailure(w, &failure{404, "not_found", "Run capture tidak ditemukan."})
		return
	}
	after := 0
	if raw := r.URL.Query().Get("after_seq"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 0 {
			writeFailure(w, &failure{400, "invalid_request", "after_seq tidak valid."})
			return
		}
		after = value
	}
	rows, listErr := h.repo.listTranslations(ctx, run, after, token)
	if listErr != nil {
		writeFailure(w, listErr)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"translations": rows})
}
