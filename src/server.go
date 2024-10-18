package main

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/joho/godotenv"
	"github.com/labstack/echo"
)

type server struct {
	config struct {
		port string
	}
	db *sql.DB
	e  *echo.Echo
}

func initServer(e *echo.Echo) (server, error) {
	s := server{e: e}

	if err := godotenv.Load("config.env"); err != nil {
		s.e.Logger.Warnf("Error loading .env file: %v", err)
	}

	if s.config.port = os.Getenv("PORT"); s.config.port == "" {
		s.config.port = "8080"
	}

	db, err := sql.Open("postgres", fmt.Sprintf(
		"postgres://%s:%s@%s/%s",
		os.Getenv("DB_USERNAME"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DATABASE_URL"),
		os.Getenv("DATABASE_NAME"),
	))
	if err != nil {
		return s, err
	}
	s.db = db

	return s, nil
}
func (s *server) Start() {
	s.e.Logger.Fatal(s.e.Start(":" + s.config.port))
}
