package server

import (
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                          string
	MatchmakingPairingMinInterval time.Duration
	MatchmakingGCMinInterval      time.Duration
	FlyAPIHostname                string
	FlyAPIKey                     string
	FlyAppName                    string
	Endpoint                      string
	RedisURL                      string
	DatabaseURL                   string
	HCLOUDToken                   string
	AWSAccessKeyID                string
	AWSSecretAccessKey            string
	AWSRegion                     string
	AWSBucketName                 string
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

	if cfg.Endpoint = os.Getenv("ENDPOINT"); cfg.Endpoint == "" {
		cfg.Endpoint = "https://" + cfg.FlyAppName + ".fly.dev"
	}

	if matchmakingInterval, err := time.ParseDuration(os.Getenv("MATCHMAKING_INTERVAL")); err == nil && matchmakingInterval > 0 {
		cfg.MatchmakingPairingMinInterval = matchmakingInterval
	} else {
		cfg.MatchmakingPairingMinInterval = 100 * time.Millisecond
	}

	if matchGCInterval, err := time.ParseDuration(os.Getenv("MATCH_GC_INTERVAL")); err == nil && matchGCInterval > 0 {
		cfg.MatchmakingGCMinInterval = matchGCInterval
	} else {
		cfg.MatchmakingGCMinInterval = 1 * time.Minute
	}

	if cfg.RedisURL = os.Getenv("REDIS_URL"); cfg.RedisURL == "" {
		return nil, fmt.Errorf("REDIS_URL is not set")
	}

	if cfg.DatabaseURL = os.Getenv("DATABASE_URL"); cfg.DatabaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL is not set")
	}

	if cfg.HCLOUDToken = os.Getenv("HCLOUD_TOKEN"); cfg.HCLOUDToken == "" {
		return nil, fmt.Errorf("HCLOUD_TOKEN is not set")
	}

	if cfg.AWSAccessKeyID = os.Getenv("AWS_ACCESS_KEY_ID"); cfg.AWSAccessKeyID == "" {
		return nil, fmt.Errorf("AWS_ACCESS_KEY_ID is not set")
	}

	if cfg.AWSSecretAccessKey = os.Getenv("AWS_SECRET_ACCESS_KEY"); cfg.AWSSecretAccessKey == "" {
		return nil, fmt.Errorf("AWS_SECRET_ACCESS_KEY is not set")
	}

	if cfg.AWSRegion = os.Getenv("AWS_REGION"); cfg.AWSRegion == "" {
		return nil, fmt.Errorf("AWS_REGION is not set")
	}

	if cfg.AWSBucketName = os.Getenv("AWS_BUCKET_NAME"); cfg.AWSBucketName == "" {
		return nil, fmt.Errorf("AWS_BUCKET_NAME is not set")
	}

	return cfg, nil
}
