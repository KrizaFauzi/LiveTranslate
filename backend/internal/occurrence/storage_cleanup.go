package occurrence

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
)

func (repo repository) occurrenceObjectPaths(ctx context.Context, occurrenceID, userID, token string) ([]string, *failure) {
	if repo.configErr != nil {
		return nil, &failure{503, "supabase_config", repo.configErr.Error()}
	}
	target := *repo.base
	target.Path = "/rest/v1/documents"
	target.RawQuery = url.Values{
		"select":        {"object_path"},
		"occurrence_id": {"eq." + occurrenceID},
		"order":         {"id.asc"},
		"limit":         {"10"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, &failure{500, "internal_error", "Permintaan dokumen tidak dapat dibuat."}
	}
	req.Header.Set("apikey", repo.key)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := repo.client.Do(req)
	if err != nil {
		return nil, &failure{502, "archive_unreachable", "Dokumen tidak dapat dijangkau."}
	}
	defer resp.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if readErr != nil || len(data) > 1<<20 || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &failure{502, "archive_response_invalid", "Respons dokumen tidak valid."}
	}
	var rows []struct {
		ObjectPath string `json:"object_path"`
	}
	if json.Unmarshal(data, &rows) != nil {
		return nil, &failure{502, "archive_response_invalid", "Respons dokumen tidak valid."}
	}
	prefix := userID + "/" + occurrenceID + "/"
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		if !strings.HasPrefix(row.ObjectPath, prefix) || len(row.ObjectPath) > 1024 {
			return nil, &failure{502, "archive_response_invalid", "Respons dokumen tidak valid."}
		}
		paths = append(paths, row.ObjectPath)
	}
	return paths, nil
}

func cleanupStorageObjects(ctx context.Context, repo repository, paths []string, token string) *failure {
	if len(paths) == 0 {
		return nil
	}
	target := *repo.base
	target.Path = "/storage/v1/object/meeting-documents"
	payload, _ := json.Marshal(map[string]any{"prefixes": paths})
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), bytes.NewReader(payload))
	if err != nil {
		return &failure{500, "internal_error", "Permintaan penghapusan file tidak dapat dibuat."}
	}
	req.Header.Set("apikey", repo.key)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, requestErr := repo.client.Do(req)
	if requestErr != nil {
		return &failure{502, "storage_cleanup_pending", "Data dihapus; pembersihan file masih antre."}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &failure{502, "storage_cleanup_pending", "Data dihapus; pembersihan file masih antre."}
	}

	completeTarget := *repo.base
	completeTarget.Path = "/rest/v1/rpc/complete_storage_cleanup_jobs"
	completeBody, _ := json.Marshal(map[string]any{"cleaned_paths": paths})
	completeRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, completeTarget.String(), bytes.NewReader(completeBody))
	if err != nil {
		return &failure{500, "internal_error", "Konfirmasi pembersihan file tidak dapat dibuat."}
	}
	completeRequest.Header.Set("apikey", repo.key)
	completeRequest.Header.Set("Authorization", "Bearer "+token)
	completeRequest.Header.Set("Content-Type", "application/json")
	completeResponse, requestErr := repo.client.Do(completeRequest)
	if requestErr != nil {
		return &failure{502, "storage_cleanup_pending", "Data dihapus; konfirmasi pembersihan masih antre."}
	}
	defer completeResponse.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(completeResponse.Body, 1<<20))
	if completeResponse.StatusCode < 200 || completeResponse.StatusCode >= 300 {
		return &failure{502, "storage_cleanup_pending", "Data dihapus; konfirmasi pembersihan masih antre."}
	}
	return nil
}
