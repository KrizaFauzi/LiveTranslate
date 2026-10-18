package services

import (
	"context"
	"livetranslate/backend/internal/config"
	"testing"
)

func TestSupabaseCheckRequiresAuthService(t *testing.T) {
	err := CheckSupabase(context.Background(), config.Config{DatabaseURL: "postgresql://must-not-connect"})
	if err == nil || err.Error() != "konfigurasi wajib: AUTH_SERVICE_URL" {
		t.Fatalf("got %v", err)
	}
}
