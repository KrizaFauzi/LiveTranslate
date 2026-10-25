package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"livetranslate/backend/internal/storagecleanup"
)

func main() {
	cleaner, err := storagecleanup.New(os.Getenv("SUPABASE_URL"), os.Getenv("SUPABASE_SECRET_KEY"), nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	count, err := cleaner.Run(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("storage cleanup: %d object(s)\n", count)
}
