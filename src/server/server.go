package server

import (
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/labstack/echo"
)

type Server struct {
	Config struct {
		port string
	}
	Logger *slog.Logger
	DB     *sql.DB
	e      *echo.Echo
}

var S *Server

func InitServer(e *echo.Echo) (Server, error) {
	S = &Server{e: e}

	S.Logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := godotenv.Load("config.env"); err != nil {
		S.e.Logger.Warnf("Error loading .env file: %v", err)
	}

	if S.Config.port = os.Getenv("PORT"); S.Config.port == "" {
		S.Config.port = "8080"
	}

	db, err := sql.Open("pgx", fmt.Sprintf(
		"postgres://%s:%s@%s/%s",
		os.Getenv("DB_USERNAME"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DATABASE_URL"),
		os.Getenv("DATABASE_NAME"),
	))
	if err != nil {
		return *S, err
	}
	S.DB = db

	return *S, nil
}
func (s *Server) Start() {
	s.e.Logger.Fatal(S.e.Start(":" + S.Config.port))
}
