package server

import (
	"context"
	"log/slog"
	"os"

	"github.com/andy98725/elo-service/src/cert"
	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/external/cloudflare"
	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/external/redis"
	"github.com/labstack/echo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Server struct {
	Config   *Config
	Logger   *slog.Logger
	DB       *gorm.DB
	Redis    *redis.Redis
	AWS      StorageService
	Machines MachineService
	// DNS and Cert are nil unless WildcardTLSEnabled() is true. Callers must
	// nil-check before use. This keeps the feature opt-in without sprinkling
	// no-op stubs everywhere.
	DNS      DNSService
	Cert     CertService
	e        *echo.Echo
	Shutdown chan struct{}
}

var S *Server

func InitServer(e *echo.Echo) (Server, error) {
	S = &Server{e: e}

	S.Shutdown = make(chan struct{})
	S.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(S.Logger)
	cfg, err := InitConfig()
	if err != nil {
		return *S, err
	}
	S.Config = cfg

	// Redis
	S.Redis, err = redis.NewRedis(S.Config.RedisURL)
	if err != nil {
		return *S, err
	}
	slog.Info("Redis connected")

	// Postgres DB
	db, err := gorm.Open(postgres.Open(S.Config.DatabaseURL), &gorm.Config{})
	if err != nil {
		return *S, err
	}
	S.DB = db
	S.Logger.Info("Database connected")

	// AWS
	S.AWS, err = aws.InitAWSClient(S.Config.AWSAccessKeyID, S.Config.AWSSecretAccessKey, S.Config.AWSRegion, S.Config.AWSBucketName)
	if err != nil {
		return *S, err
	}
	slog.Info("AWS connected")

	// Hetzner
	hetzner, err := hetzner.InitHetznerConnection(S.Config.HCLOUDToken)
	if err != nil {
		return *S, err
	}
	S.Machines = hetzner
	if err := S.Machines.ValidateServerType(context.Background(), S.Config.HCLOUDHostType); err != nil {
		return *S, err
	}
	slog.Info("Hetzner connected")

	// Wildcard-TLS subsystem (optional). When the env vars are missing the
	// matchmaker keeps using raw IPs and skips all cert/DNS work.
	if S.Config.WildcardTLSEnabled() {
		S.DNS = cloudflare.New(S.Config.CloudflareAPIToken, S.Config.CloudflareZoneID)

		mgr, err := cert.New(cert.Config{
			Domain:          S.Config.GameServerDomain,
			CloudflareToken: S.Config.CloudflareAPIToken,
			Email:           S.Config.CertEmail,
			Storage:         S.AWS,
			IsNotFound:      isCertStorageNotFound,
		})
		if err != nil {
			return *S, err
		}
		// EnsureFresh blocks on first run (it issues the cert via DNS-01).
		// That can take ~30s; we accept that cost at startup so by the time
		// matchmaking runs, the cert is loaded and any new host's cloud-init
		// can inline it.
		if err := mgr.EnsureFresh(context.Background()); err != nil {
			return *S, err
		}
		S.Cert = mgr
		slog.Info("Wildcard TLS subsystem ready", "domain", "*."+S.Config.GameServerDomain)
	} else {
		slog.Info("Wildcard TLS disabled (legacy IP-based mode)")
	}

	return *S, nil
}

// isCertStorageNotFound bridges the cert package's IsNotFound expectation
// to the AWS client's sentinel. Lives here so cert/manager.go doesn't
// depend on the AWS package directly.
func isCertStorageNotFound(err error) bool {
	return err == aws.ErrNotFound
}

func (s *Server) Start() {
	s.e.Logger.Fatal(S.e.Start(":" + S.Config.Port))
}
