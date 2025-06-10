package server

import (
	"errors"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/labstack/echo"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Server struct {
	Config struct {
		port                string
		WorkerSleepDuration time.Duration
	}
	Logger   *slog.Logger
	DB       *gorm.DB
	Redis    *redis.Client
	e        *echo.Echo
	Shutdown chan struct{}
}

var S *Server

func InitServer(e *echo.Echo) (Server, error) {
	S = &Server{e: e, Shutdown: make(chan struct{})}
	S.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(S.Logger)

	if err := godotenv.Load("config.env"); err != nil {
		slog.Warn("Error loading config.env file", "error", err)
	}
	if S.Config.port = os.Getenv("PORT"); S.Config.port == "" {
		S.Config.port = "8080"
	}

	if workerSleep, err := time.ParseDuration(os.Getenv("WORKER_SLEEP_DURATION")); err == nil && workerSleep > 0 {
		S.Config.WorkerSleepDuration = workerSleep
	} else {
		S.Config.WorkerSleepDuration = 1 * time.Second
	}

	// Redis
	opt, err := redis.ParseURL(os.Getenv("REDIS_URL"))
	if err != nil {
		log.Fatalf("Failed to parse Redis URL: %v", err)
	}
	S.Redis = redis.NewClient(opt)
	slog.Info("Redis connected")

	// Postgres DB
	if os.Getenv("DATABASE_URL") == "" {
		return *S, errors.New("DATABASE_URL is not set")
	}
	slog.Info("DATABASE_URL: " + os.Getenv("DATABASE_URL"))

	db, err := gorm.Open(postgres.Open(os.Getenv("DATABASE_URL")), &gorm.Config{})
	if err != nil {
		return *S, err
	}
	S.DB = db
	S.Logger.Info("Database connected")

	return *S, nil
}

func (s *Server) Start() {
	s.e.Logger.Fatal(S.e.Start(":" + S.Config.port))
}
