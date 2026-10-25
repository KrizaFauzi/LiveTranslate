package audio

import (
	"net/http"
	"testing"
)

// The saved archive has its own authorization boundary, independent of live SSE.
func TestSavedTranscriptRequiresOwnerAuthorization(t *testing.T) {
	h, _ := newHandler(t, workingProvider{})
	for _, scenario := range []struct {
		name  string
		token string
		want  int
	}{
		{"anonymous", "", http.StatusUnauthorized},
		{"foreign account", "other", http.StatusNotFound},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			response := call(t, h, http.MethodGet, "/api/audio/runs/"+runID+"/segments", "", scenario.token, "")
			if response.Code != scenario.want {
				t.Fatalf("saved transcript authorization: got %d, want %d; body=%s", response.Code, scenario.want, response.Body.String())
			}
		})
	}
}
