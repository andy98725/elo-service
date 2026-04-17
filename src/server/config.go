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
	HCLOUDHostType                string
	HCLOUDMaxHosts                int
	HCLOUDMaxSlotsPerHost         int
	HCLOUDAgentPort               int64
	HCLOUDPortRangeStart          int64
	HCLOUDPortRangeEnd            int64
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

	if cfg.HCLOUDHostType = os.Getenv("HCLOUD_HOST_TYPE"); cfg.HCLOUDHostType == "" {
		cfg.HCLOUDHostType = "cx32"
	}

	cfg.HCLOUDMaxHosts = 5
	if v := os.Getenv("HCLOUD_MAX_HOSTS"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDMaxHosts); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_MAX_HOSTS must be an integer")
		}
	}

	cfg.HCLOUDMaxSlotsPerHost = 8
	if v := os.Getenv("HCLOUD_MAX_SLOTS_PER_HOST"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDMaxSlotsPerHost); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_MAX_SLOTS_PER_HOST must be an integer")
		}
	}

	cfg.HCLOUDAgentPort = 8080
	if v := os.Getenv("HCLOUD_AGENT_PORT"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDAgentPort); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_AGENT_PORT must be an integer")
		}
	}

	cfg.HCLOUDPortRangeStart = 7000
	if v := os.Getenv("HCLOUD_PORT_RANGE_START"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDPortRangeStart); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_PORT_RANGE_START must be an integer")
		}
	}

	cfg.HCLOUDPortRangeEnd = 9000
	if v := os.Getenv("HCLOUD_PORT_RANGE_END"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDPortRangeEnd); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_PORT_RANGE_END must be an integer")
		}
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
