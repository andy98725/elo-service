package server

import (
	"log/slog"
	"os"

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
	Machines *hetzner.HetznerConnection
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

	// Hetzner
	hetzner, err := hetzner.InitHetznerConnection(S.Config.HCLOUDToken)
	if err != nil {
		return *S, err
	}
	S.Machines = hetzner
	slog.Info("Hetzner connected")

	return *S, nil
}

func (s *Server) Start() {
	s.e.Logger.Fatal(S.e.Start(":" + S.Config.Port))
}
