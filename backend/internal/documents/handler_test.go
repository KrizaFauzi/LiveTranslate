package documents

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"livetranslate/backend/internal/auth"
)

func TestUploadRejectsRequestLargerThanDocumentEnvelope(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("occurrence_id", "aaaaaaaa-2222-4333-8444-555555555555")
	part, err := writer.CreateFormFile("file", "large.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(bytes.Repeat([]byte{'x'}, int(maxDocumentBytes+maxMultipartOverhead))); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	h := &handler{verify: func(context.Context, string) (string, *auth.VerificationError) { return "owner", nil }}
	req := httptest.NewRequest(http.MethodPost, "/api/documents", &body)
	req.Header.Set("Authorization", "Bearer owner")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	h.ServeHTTP(response, req)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "document_too_large") {
		t.Fatalf("oversized multipart: %d %s", response.Code, response.Body.String())
	}
}
