package audio

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

const translationColumns = "id,user_id,meeting_id,occurrence_id,run_id,source_segment_id,source_seq,source_revision,language,text,status,error_code,created_at,updated_at"

type Translation struct {
	ID              string  `json:"id"`
	UserID          string  `json:"user_id"`
	MeetingID       string  `json:"meeting_id"`
	OccurrenceID    string  `json:"occurrence_id"`
	RunID           string  `json:"run_id"`
	SourceSegmentID string  `json:"source_segment_id"`
	SourceSeq       int     `json:"source_seq"`
	SourceRevision  int     `json:"source_revision"`
	Language        string  `json:"language"`
	Text            *string `json:"text"`
	Status          string  `json:"status"`
	ErrorCode       *string `json:"error_code"`
	CreatedAt       string  `json:"created_at"`
	UpdatedAt       string  `json:"updated_at"`
}

func translationInvalid() *failure {
	return &failure{502, "translation_response_invalid", "Respons terjemahan tidak valid."}
}

func decodeTranslations(data []byte, run Run) ([]Translation, *failure) {
	var rows []Translation
	if json.Unmarshal(data, &rows) != nil {
		return nil, translationInvalid()
	}
	for _, row := range rows {
		validResult := row.Status == "success" && row.Text != nil && *row.Text != "" && row.ErrorCode == nil
		validResult = validResult || (row.Status == "failed" && row.Text == nil && row.ErrorCode != nil && *row.ErrorCode != "")
		if !uuidPattern.MatchString(row.ID) || !uuidPattern.MatchString(row.SourceSegmentID) || row.UserID != run.UserID || row.MeetingID != run.MeetingID || row.OccurrenceID != run.OccurrenceID || row.RunID != run.ID || row.SourceSeq <= 0 || row.SourceRevision <= 0 || row.Language != "id" || !validResult {
			return nil, translationInvalid()
		}
	}
	return rows, nil
}

func (repo repository) putTranslation(ctx context.Context, run Run, seq int, text, status, errorCode, token string) (bool, *failure) {
	body := map[string]any{"run_uuid": run.ID, "segment_seq": seq, "translated_text": text, "translation_status": status, "translation_error_code": nil, "translation_revision": 1}
	if errorCode != "" {
		body["translation_error_code"] = errorCode
	}
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/put_translation", nil, body, token)
	if err != nil {
		return false, err
	}
	rows, decodeErr := decodeTranslations(data, run)
	if decodeErr != nil {
		return false, decodeErr
	}
	return len(rows) == 1 && rows[0].SourceSeq == seq && rows[0].Status == status, nil
}

func (repo repository) failMissingTranslations(ctx context.Context, run Run, token string) *failure {
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/fail_missing_translations", nil, map[string]any{
		"run_uuid": run.ID, "failure_code": "translation_queue_overflow",
	}, token)
	if err != nil {
		return err
	}
	var count int
	if json.Unmarshal(data, &count) != nil || count < 0 {
		return translationInvalid()
	}
	return nil
}

func (repo repository) listTranslations(ctx context.Context, run Run, after int, token string) ([]Translation, *failure) {
	query := url.Values{"select": {translationColumns}, "run_id": {"eq." + run.ID}, "source_seq": {"gt." + strconv.Itoa(after)}, "order": {"source_seq.asc,source_revision.desc"}, "limit": {"200"}}
	data, err := repo.send(ctx, "GET", "/rest/v1/translations", query, nil, token)
	if err != nil {
		return nil, err
	}
	return decodeTranslations(data, run)
}
