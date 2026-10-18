package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const ConnectionTimeout = 10 * time.Second
const AITimeout = 30 * time.Second

type Config struct {
	AuthServiceURL    string
	AuthInternalToken string
	DatabaseURL       string
	DeepgramKey       string
	DeepgramModel     string
	SumopodBaseURL    string
	SumopodKey        string
	SumopodModel      string
}

func Load() Config {
	base := os.Getenv("SUMOPOD_BASE_URL")
	if base == "" {
		base = "https://ai.sumopod.com/v1"
	}
	return Config{
		AuthServiceURL: os.Getenv("AUTH_SERVICE_URL"), AuthInternalToken: os.Getenv("AUTH_INTERNAL_TOKEN"),
		DatabaseURL: os.Getenv("DATABASE_URL"), DeepgramKey: os.Getenv("DEEPGRAM_API_KEY"),
		DeepgramModel: os.Getenv("DEEPGRAM_MODEL"), SumopodBaseURL: base,
		SumopodKey: os.Getenv("SUMOPOD_API_KEY"), SumopodModel: os.Getenv("SUMOPOD_MODEL"),
	}
}

func Require(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("konfigurasi wajib: %s", name)
	}
	return nil
}
