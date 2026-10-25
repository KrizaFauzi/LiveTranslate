package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"livetranslate/backend/internal/assistance"
	"livetranslate/backend/internal/audio"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/deepgram"
	"livetranslate/backend/internal/documents"
	"livetranslate/backend/internal/health"
	"livetranslate/backend/internal/meeting"
	"livetranslate/backend/internal/occurrence"
	"livetranslate/backend/internal/sumopod"
	"livetranslate/backend/internal/transcribe"
	"log"
	"net/http"
	"os"
	"time"
)

func main() {
	check := flag.String("check", "", "deepgram|sumopod; menjalankan satu pemeriksaan lalu keluar")
	audio := flag.String("audio", "", "file audio untuk pemeriksaan Deepgram")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "argumen tidak dikenal")
		os.Exit(2)
	}
	c := config.Load()
	var err error
	switch *check {
	case "":
		server := &http.Server{Addr: ":8080", Handler: newHandler(c), ReadHeaderTimeout: config.ConnectionTimeout, ReadTimeout: config.ConnectionTimeout, WriteTimeout: 2 * config.ConnectionTimeout}
		fmt.Println("LiveTranslate backend mendengarkan :8080")
		if server.ListenAndServe() != nil {
			fmt.Fprintln(os.Stderr, "server gagal dijalankan")
			os.Exit(1)
		}
		return
	case "deepgram":
		err = deepgram.Check(context.Background(), c, *audio)
	case "sumopod":
		err = sumopod.Check(context.Background(), c)
	default:
		fmt.Fprintln(os.Stderr, "-check tidak dikenal; gunakan deepgram|sumopod")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("%s: LULUS\n", *check)
}

func newHandler(c config.Config) http.Handler {
	mux := http.NewServeMux()
	health.RegisterRoutes(mux, c)
	auth.RegisterRoutes(mux, c)
	meeting.RegisterRoutes(mux, c)
	occurrence.RegisterRoutes(mux, c)
	documents.RegisterRoutes(mux, c)
	assistance.RegisterRoutes(mux, c)
	broker := transcribe.NewBroker(c, nil)
	audio.RegisterRoutes(mux, c, broker)
	return operationalMiddleware(mux)
}

type observedWriter struct {
	http.ResponseWriter
	status int
}

func (w *observedWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *observedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func operationalMiddleware(next http.Handler) http.Handler {
	slots := make(chan struct{}, 64)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, `{"error":{"code":"overloaded","message":"Server sedang sibuk."}}`, http.StatusServiceUnavailable)
			return
		}
		var raw [16]byte
		_, _ = rand.Read(raw[:])
		traceID := hex.EncodeToString(raw[:])
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Trace-ID", traceID)
		start := time.Now()
		observed := &observedWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(observed, r)
		log.Printf("trace_id=%s method=%s path=%q status=%d duration_ms=%d", traceID, r.Method, r.URL.Path, observed.status, time.Since(start).Milliseconds())
	})
}
