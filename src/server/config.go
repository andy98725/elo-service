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
	// MatchCooldownDuration is how long after /result/report the game
	// container, host ports, and match auth_code remain alive before the
	// worker GC sweep tears them down. The cooldown lets the game server
	// finish post-match work (artifact uploads, server-authored
	// player_data writes) without racing teardown. Zero disables the
	// cooldown entirely — the report path runs phase A and phase B
	// back-to-back, matching pre-cooldown behavior.
	MatchCooldownDuration time.Duration
	// MatchCooldownForceDeadline is the absolute cap on how long an
	// instance may sit in cooldown before the sweep force-finalizes it
	// regardless of agent reachability. Measured from MatchResult.CreatedAt
	// (i.e. when the result was reported), so it includes the cooldown
	// window itself. After this point: ports are freed, the SI is marked
	// deleted, and the Match row is dropped, even if the agent stop call
	// kept failing. Guards against a permanently-broken agent leaking
	// ports/rows.
	MatchCooldownForceDeadline    time.Duration
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
	HCLOUDWarmSlots               int
	HCLOUDAgentPort               int64
	HCLOUDPortRangeStart          int64
	HCLOUDPortRangeEnd            int64
	AWSAccessKeyID                string
	AWSSecretAccessKey            string
	AWSRegion                     string
	AWSBucketName                 string

	// Wildcard-TLS feature: when all three are set, the matchmaker maintains
	// a single *.${GameServerDomain} cert via Let's Encrypt DNS-01, creates
	// per-host A records on host provisioning, and tells clients to connect
	// via the hostname (so WebGL builds can use wss://). Leave any empty to
	// disable; clients fall back to ip:port and no DNS records are created.
	GameServerDomain    string // e.g. "gs.elomm.net" — wildcard cert covers *.<this>
	CloudflareAPIToken  string
	CloudflareZoneID    string
	CertEmail           string        // ACME registration contact (default admin@<GameServerDomain>)
	CertRenewalInterval time.Duration // worker tick interval; default 12h

	// WSLivenessDisconnectEnabled gates whether the WebSocket ping/pong
	// keepalive on /match/join and /lobby/* actually drops idle clients.
	// When false (default during the rollout window), the server still pings
	// and tracks pong arrivals but only logs a warning on missed pongs —
	// clients that haven't been updated yet stay connected. Flip to true once
	// staging telemetry shows the warning rate has dropped to acceptable
	// levels; idle clients then get disconnected after WSLivenessPongGrace.
	WSLivenessDisconnectEnabled bool
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

	// Negative is rejected; zero is meaningful (disable cooldown). Use
	// raw os.Getenv presence — not ParseDuration's err — to distinguish
	// "unset" (apply default) from "set to 0" (disable).
	if v := os.Getenv("MATCH_COOLDOWN_DURATION"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("MATCH_COOLDOWN_DURATION must be a non-negative duration")
		}
		cfg.MatchCooldownDuration = d
	} else {
		cfg.MatchCooldownDuration = 5 * time.Minute
	}

	if v := os.Getenv("MATCH_COOLDOWN_FORCE_DEADLINE"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nil, fmt.Errorf("MATCH_COOLDOWN_FORCE_DEADLINE must be a positive duration")
		}
		cfg.MatchCooldownForceDeadline = d
	} else {
		cfg.MatchCooldownForceDeadline = 30 * time.Minute
	}
	if cfg.MatchCooldownForceDeadline < cfg.MatchCooldownDuration {
		return nil, fmt.Errorf("MATCH_COOLDOWN_FORCE_DEADLINE (%s) must be >= MATCH_COOLDOWN_DURATION (%s)",
			cfg.MatchCooldownForceDeadline, cfg.MatchCooldownDuration)
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
		cfg.HCLOUDHostType = "cx33"
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

	cfg.HCLOUDWarmSlots = 0
	if v := os.Getenv("HCLOUD_WARM_SLOTS"); v != "" {
		if n, err := fmt.Sscanf(v, "%d", &cfg.HCLOUDWarmSlots); n != 1 || err != nil {
			return nil, fmt.Errorf("HCLOUD_WARM_SLOTS must be an integer")
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

	// Wildcard-TLS optional config. All-or-nothing: if any of the three is
	// set, the other two must be set too — otherwise we'd have inconsistent
	// state (DNS records being created with no cert, or vice versa).
	cfg.GameServerDomain = os.Getenv("GAME_SERVER_DOMAIN")
	cfg.CloudflareAPIToken = os.Getenv("CLOUDFLARE_API_TOKEN")
	cfg.CloudflareZoneID = os.Getenv("CLOUDFLARE_ZONE_ID")
	wildcardCount := 0
	for _, v := range []string{cfg.GameServerDomain, cfg.CloudflareAPIToken, cfg.CloudflareZoneID} {
		if v != "" {
			wildcardCount++
		}
	}
	if wildcardCount != 0 && wildcardCount != 3 {
		return nil, fmt.Errorf("wildcard-TLS config is partial: GAME_SERVER_DOMAIN, CLOUDFLARE_API_TOKEN, and CLOUDFLARE_ZONE_ID must all be set together (got %d/3)", wildcardCount)
	}
	if cfg.CertEmail = os.Getenv("CERT_EMAIL"); cfg.CertEmail == "" && cfg.GameServerDomain != "" {
		cfg.CertEmail = "admin@" + cfg.GameServerDomain
	}
	if cri, err := time.ParseDuration(os.Getenv("CERT_RENEWAL_INTERVAL")); err == nil && cri > 0 {
		cfg.CertRenewalInterval = cri
	} else {
		cfg.CertRenewalInterval = 12 * time.Hour
	}

	cfg.WSLivenessDisconnectEnabled = os.Getenv("WS_LIVENESS_DISCONNECT_ENABLED") == "true"

	return cfg, nil
}

// WildcardTLSEnabled reports whether all three required env vars are set.
// Callers branch on this to skip cert/DNS work when running locally or in
// tests without the production secrets.
func (c *Config) WildcardTLSEnabled() bool {
	return c.GameServerDomain != "" && c.CloudflareAPIToken != "" && c.CloudflareZoneID != ""
}
