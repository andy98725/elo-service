package server

import (
	"errors"
	"log"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	"github.com/labstack/echo"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Server struct {
	Config struct {
		port string
	}
	Logger *slog.Logger
	DB     *gorm.DB
	Redis  *redis.Client
	e      *echo.Echo
}

var S *Server

func InitServer(e *echo.Echo) (Server, error) {
	S = &Server{e: e}
	S.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(S.Logger)

	if err := godotenv.Load("config.env"); err != nil {
		slog.Warn("Error loading config.env file", "error", err)
	}
	if S.Config.port = os.Getenv("PORT"); S.Config.port == "" {
		S.Config.port = "8080"
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
