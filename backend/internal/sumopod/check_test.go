package sumopod

import (
	"context"
	"livetranslate/backend/internal/config"
	"strings"
	"testing"
)

func TestSumopodRejectsInvalidConfigBeforeNetwork(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.Config
		want string
	}{
		{"missing key", config.Config{}, "SUMOPOD_API_KEY"},
		{"missing model", config.Config{SumopodKey: "test-secret"}, "SUMOPOD_MODEL"},
		{"wrong provider", config.Config{SumopodKey: "test-secret", SumopodModel: "account-model", SumopodBaseURL: "https://example.com/v1"}, "SUMOPOD_BASE_URL"},
		{"plaintext", config.Config{SumopodKey: "test-secret", SumopodModel: "account-model", SumopodBaseURL: "http://ai.sumopod.com/v1"}, "SUMOPOD_BASE_URL"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := Check(context.Background(), tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "test-secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
