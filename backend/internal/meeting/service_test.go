package meeting

import (
	"encoding/json"
	"os"
	"testing"
)

func TestMeetingURLFixtures(t *testing.T) {
	data, err := os.ReadFile("../../../fixtures/meeting-urls.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		URL       string `json:"url"`
		Key       string `json:"key"`
		Canonical string `json:"canonical"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		t.Run(f.URL, func(t *testing.T) {
			value, err := classifyURL(f.URL)
			if f.Key == "" {
				if err == nil {
					t.Fatal("unsupported URL accepted")
				}
				return
			}
			if err != nil || value.MeetingKey != f.Key || value.CanonicalURL != f.Canonical {
				t.Fatalf("got %+v, %v", value, err)
			}
		})
	}
}
