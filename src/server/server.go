package server

import (
	"context"
	"log"
	"log/slog"
	"os"

	"github.com/labstack/echo"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Server struct {
	Config   *Config
	Logger   *slog.Logger
	DB       *gorm.DB
	Redis    *Redis
	Machines *HetznerConnection
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
	opt, err := redis.ParseURL(S.Config.RedisURL)
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	S.Redis = NewRedis(opt)
	if err := S.Redis.Client.Ping(context.Background()).Err(); err != nil {
		log.Fatalf("Redis ping failed (is Redis running and REDIS_URL correct?): %v", err)
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
	hetzner, err := InitHetznerConnection(S.Config.HCLOUDToken)
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
