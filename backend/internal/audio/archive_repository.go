package audio

import (
	"context"
	"encoding/json"
	"math"
	"net/url"
	"strconv"
	"time"

	"livetranslate/backend/internal/transcribe"
)

const segmentColumns = "id,user_id,meeting_id,run_id,seq,text,start,duration,language,created_at"

type storedSegment struct {
	ID        string  `json:"id"`
	UserID    string  `json:"user_id"`
	MeetingID string  `json:"meeting_id"`
	RunID     string  `json:"run_id"`
	Seq       int     `json:"seq"`
	Text      string  `json:"text"`
	Start     float64 `json:"start"`
	Duration  float64 `json:"duration"`
	Language  string  `json:"language"`
	CreatedAt string  `json:"created_at"`
}

func archiveInvalid() *failure {
	return &failure{502, "transcript_response_invalid", "Respons arsip transkrip tidak valid."}
}

func decodeSegments(data []byte, run Run) ([]storedSegment, *failure) {
	var rows []storedSegment
	if json.Unmarshal(data, &rows) != nil {
		return nil, archiveInvalid()
	}
	for _, row := range rows {
		if !uuidPattern.MatchString(row.ID) || row.UserID != run.UserID || row.MeetingID != run.MeetingID || row.RunID != run.ID || row.Seq <= 0 || row.Text == "" || row.Start < 0 || row.Duration < 0 || math.IsNaN(row.Start) || math.IsNaN(row.Duration) || math.IsInf(row.Start, 0) || math.IsInf(row.Duration, 0) {
			return nil, archiveInvalid()
		}
	}
	return rows, nil
}

func (repo repository) appendFinal(ctx context.Context, run Run, segment transcribe.Segment, token string) (bool, *failure) {
	body := map[string]any{"run_uuid": run.ID, "segment_seq": segment.Seq, "segment_text": segment.Text, "segment_start": segment.Start, "segment_duration": segment.Duration, "segment_language": segment.Language}
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/append_transcript_final", nil, body, token)
	if err != nil {
		return false, err
	}
	rows, err := decodeSegments(data, run)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return false, nil
	}
	if len(rows) != 1 || rows[0].Seq != segment.Seq || rows[0].Text != segment.Text || rows[0].Start != segment.Start || rows[0].Duration != segment.Duration || rows[0].Language != segment.Language {
		return false, archiveInvalid()
	}
	return true, nil
}

func (repo repository) transcriptMutation(ctx context.Context, run Run, token, rpc string, body map[string]any) (Run, bool, *failure) {
	data, err := repo.send(ctx, "POST", "/rest/v1/rpc/"+rpc, nil, body, token)
	if err != nil {
		return Run{}, false, err
	}
	rows, err := decodeRuns(data, run.UserID)
	if err != nil {
		return Run{}, false, err
	}
	if len(rows) == 0 {
		return Run{}, false, nil
	}
	if len(rows) != 1 || rows[0].ID != run.ID || rows[0].MeetingID != run.MeetingID {
		return Run{}, false, archiveInvalid()
	}
	return rows[0], true, nil
}

func (repo repository) setTranscriptState(ctx context.Context, run Run, token, state string) (bool, *failure) {
	updated, found, err := repo.transcriptMutation(ctx, run, token, "set_transcript_state", map[string]any{"run_uuid": run.ID, "state_value": state})
	if err != nil || !found {
		return false, err
	}
	if updated.TranscriptState != state {
		return false, archiveInvalid()
	}
	return true, nil
}

func (repo repository) deleteTranscript(ctx context.Context, run Run, token string) (Run, bool, *failure) {
	updated, found, err := repo.transcriptMutation(ctx, run, token, "delete_transcript", map[string]any{"run_uuid": run.ID})
	if err == nil && found && updated.TranscriptState != "deleted" {
		return Run{}, false, archiveInvalid()
	}
	return updated, found, err
}

func (repo repository) segmentPage(ctx context.Context, run Run, token string, after, through, limit int) ([]storedSegment, *failure) {
	query := url.Values{"select": {segmentColumns}, "run_id": {"eq." + run.ID}, "order": {"seq.asc"}, "limit": {strconv.Itoa(limit)}}
	query["seq"] = []string{"gt." + strconv.Itoa(after), "lte." + strconv.Itoa(through)}
	data, err := repo.send(ctx, "GET", "/rest/v1/transcript_segments", query, nil, token)
	if err != nil {
		return nil, err
	}
	rows, err := decodeSegments(data, run)
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return nil, archiveInvalid()
	}
	previous := after
	for _, row := range rows {
		if row.Seq <= previous || row.Seq > through {
			return nil, archiveInvalid()
		}
		previous = row.Seq
	}
	return rows, nil
}

func (repo repository) listTranscripts(ctx context.Context, user, meetingID, token string, limit int, cursor *archiveCursor, now time.Time) ([]Run, *failure) {
	query := url.Values{"select": {columns}, "meeting_id": {"eq." + meetingID}, "save_transcript": {"eq.true"}, "transcript_state": {"neq.deleted"}, "order": {"started_at.desc,id.desc"}, "limit": {strconv.Itoa(limit)}}
	if cursor != nil {
		query.Set("or", "(started_at.lt."+cursor.Started+",and(started_at.eq."+cursor.Started+",id.lt."+cursor.ID+"))")
	}
	data, err := repo.send(ctx, "GET", "/rest/v1/meeting_runs", query, nil, token)
	if err != nil {
		return nil, err
	}
	rows, err := decodeRuns(data, user)
	if err != nil {
		return nil, err
	}
	if len(rows) > limit {
		return nil, archiveInvalid()
	}
	previous := cursor
	for _, row := range rows {
		started, parseErr := time.Parse(time.RFC3339Nano, row.StartedAt)
		expiry := string(row.TranscriptExpiresAt)
		expiryValid := expiry == ""
		if expiry != "" {
			parsedExpiry, expiryErr := time.Parse(time.RFC3339Nano, expiry)
			expiryValid = expiryErr == nil && parsedExpiry.After(now)
		}
		if parseErr != nil || !expiryValid || !row.SaveTranscript || row.TranscriptState == "deleted" || row.MeetingID != meetingID {
			return nil, archiveInvalid()
		}
		if previous != nil {
			prior, _ := time.Parse(time.RFC3339Nano, previous.Started)
			if started.After(prior) || started.Equal(prior) && row.ID >= previous.ID {
				return nil, archiveInvalid()
			}
		}
		previous = &archiveCursor{Started: row.StartedAt, ID: row.ID}
	}
	return rows, nil
}

func (repo repository) maxTranscriptSeq(ctx context.Context, run Run, token string) (int, *failure) {
	query := url.Values{"select": {"user_id,meeting_id,run_id,seq"}, "run_id": {"eq." + run.ID}, "order": {"seq.desc"}, "limit": {"1"}}
	data, err := repo.send(ctx, "GET", "/rest/v1/transcript_segments", query, nil, token)
	if err != nil {
		return 0, err
	}
	var rows []struct {
		UserID    string `json:"user_id"`
		MeetingID string `json:"meeting_id"`
		RunID     string `json:"run_id"`
		Seq       int    `json:"seq"`
	}
	if json.Unmarshal(data, &rows) != nil || len(rows) > 1 {
		return 0, archiveInvalid()
	}
	if len(rows) == 0 {
		return 0, nil
	}
	row := rows[0]
	if row.UserID != run.UserID || row.MeetingID != run.MeetingID || row.RunID != run.ID || row.Seq <= 0 {
		return 0, archiveInvalid()
	}
	return row.Seq, nil
}
