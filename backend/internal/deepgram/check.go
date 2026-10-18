package deepgram

import (
	"context"
	"errors"
	"livetranslate/backend/internal/config"
	"net/http/httptrace"
	"os"
	"strings"
	"time"

	api "github.com/deepgram/deepgram-go-sdk/v3/pkg/api/listen/v1/rest"
	interfaces "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/interfaces/v1"
	listen "github.com/deepgram/deepgram-go-sdk/v3/pkg/client/listen"
)

func Check(ctx context.Context, c config.Config, audio string) error {
	if err := config.Require("DEEPGRAM_API_KEY", c.DeepgramKey); err != nil {
		return err
	}
	if err := config.Require("DEEPGRAM_MODEL", c.DeepgramModel); err != nil {
		return err
	}
	info, err := os.Stat(audio)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("Deepgram: -audio wajib menunjuk file audio tidak kosong")
	}
	ctx, cancel := context.WithTimeout(ctx, config.AITimeout)
	defer cancel()
	// Bound DNS/dial/TLS separately from the complete transcription request.
	connectionTimer := time.AfterFunc(config.ConnectionTimeout, cancel)
	defer connectionTimer.Stop()
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connectionTimer.Stop() },
	})
	client := listen.NewREST(c.DeepgramKey, &interfaces.ClientOptions{})
	result, err := api.New(client).FromFile(ctx, audio, &interfaces.PreRecordedTranscriptionOptions{Model: c.DeepgramModel})
	if err != nil {
		return errors.New("Deepgram: request gagal atau timeout")
	}
	if result != nil {
		for _, channel := range result.Results.Channels {
			for _, alternative := range channel.Alternatives {
				if strings.TrimSpace(alternative.Transcript) != "" {
					return nil
				}
			}
		}
	}
	return errors.New("Deepgram: transkrip kosong")
}
