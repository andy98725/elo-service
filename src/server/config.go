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
	MatchmakingInterval time.Duration
	MatchGCInterval     time.Duration
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

	if matchmakingInterval, err := time.ParseDuration(os.Getenv("MATCHMAKING_INTERVAL")); err == nil && matchmakingInterval > 0 {
		cfg.MatchmakingInterval = matchmakingInterval
	} else {
		cfg.MatchmakingInterval = 100 * time.Millisecond
	}

	if matchGCInterval, err := time.ParseDuration(os.Getenv("MATCH_GC_INTERVAL")); err == nil && matchGCInterval > 0 {
		cfg.MatchGCInterval = matchGCInterval
	} else {
		cfg.MatchGCInterval = 1 * time.Minute
	}

	return cfg, nil
}
