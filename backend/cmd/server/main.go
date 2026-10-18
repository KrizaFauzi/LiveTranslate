package main

import (
	"context"
	"flag"
	"fmt"
	"livetranslate/backend/internal/auth"
	"livetranslate/backend/internal/config"
	"livetranslate/backend/internal/deepgram"
	"livetranslate/backend/internal/health"
	"livetranslate/backend/internal/sumopod"
	"net/http"
	"os"
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
	health.RegisterRoutes(mux)
	auth.RegisterRoutes(mux, c)
	return mux
}
