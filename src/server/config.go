package server

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                string
	WorkerSleepDuration time.Duration
	FlyAPIHostname      string
	FlyAPIKey           string
	FlyAppName          string
}

func InitConfig() (*Config, error) {
	_ = godotenv.Load(".env")
	if err := godotenv.Load("config.env"); err != nil {
		slog.Warn("Error loading config.env file", "error", err)
	}

	cfg := &Config{}

	if cfg.Port = os.Getenv("PORT"); cfg.Port == "" {
		cfg.Port = "8080"
	}

	if cfg.FlyAPIHostname = os.Getenv("FLY_API_HOSTNAME"); cfg.FlyAPIHostname == "" {
		return nil, fmt.Errorf("FLY_API_HOSTNAME is not set")
	}

	if cfg.FlyAPIKey = os.Getenv("FLY_API_KEY"); cfg.FlyAPIKey == "" {
		return nil, fmt.Errorf("FLY_API_KEY is not set")
	}

	if cfg.FlyAppName = os.Getenv("FLY_APP_NAME"); cfg.FlyAppName == "" {
		return nil, fmt.Errorf("FLY_APP_NAME is not set")
	}

	if workerSleep, err := time.ParseDuration(os.Getenv("WORKER_SLEEP_DURATION")); err == nil && workerSleep > 0 {
		cfg.WorkerSleepDuration = workerSleep
	} else {
		cfg.WorkerSleepDuration = 1 * time.Second
	}

	return cfg, nil
}
