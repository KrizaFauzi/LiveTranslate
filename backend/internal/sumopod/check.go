package sumopod

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"livetranslate/backend/internal/config"
	"net"
	"net/http"
	"net/url"
	"strings"
)

func Check(ctx context.Context, c config.Config) error {
	if err := config.Require("SUMOPOD_API_KEY", c.SumopodKey); err != nil {
		return err
	}
	if err := config.Require("SUMOPOD_MODEL", c.SumopodModel); err != nil {
		return err
	}
	u, err := url.Parse(c.SumopodBaseURL)
	if err != nil || u.Scheme != "https" || u.Host != "ai.sumopod.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.TrimRight(u.Path, "/") != "/v1" {
		return errors.New("SUMOPOD_BASE_URL wajib https://ai.sumopod.com/v1")
	}
	payload, err := json.Marshal(struct {
		Model    string              `json:"model"`
		Messages []map[string]string `json:"messages"`
		Stream   bool                `json:"stream"`
	}{c.SumopodModel, []map[string]string{{"role": "user", "content": "Reply with OK."}}, false})
	if err != nil {
		return errors.New("Sumopod: payload gagal")
	}
	ctx, cancel := context.WithTimeout(ctx, config.AITimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.SumopodBaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return errors.New("Sumopod: request tidak valid")
	}
	req.Header.Set("Authorization", "Bearer "+c.SumopodKey)
	req.Header.Set("Content-Type", "application/json")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: config.ConnectionTimeout}).DialContext
	transport.TLSHandshakeTimeout = config.ConnectionTimeout
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: config.AITimeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("Sumopod: request gagal atau timeout")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Sumopod: HTTP %d", resp.StatusCode)
	}
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1024*1024)).Decode(&result); err != nil {
		return errors.New("Sumopod: respons tidak valid")
	}
	for _, choice := range result.Choices {
		if strings.TrimSpace(choice.Message.Content) != "" {
			return nil
		}
	}
	return errors.New("Sumopod: jawaban kosong")
}
