package server

import (
	"errors"
	"log/slog"
	"os"

	"github.com/joho/godotenv"
	"github.com/labstack/echo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Server struct {
	Config struct {
		port string
	}
	Logger *slog.Logger
	DB     *gorm.DB
	e      *echo.Echo
}

var S *Server

func InitServer(e *echo.Echo) (Server, error) {
	S = &Server{e: e}

	S.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := godotenv.Load("config.env"); err != nil {
		S.e.Logger.Warnf("Error loading config.env file: %v", err)
	}

	if S.Config.port = os.Getenv("PORT"); S.Config.port == "" {
		S.Config.port = "8080"
	}
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
