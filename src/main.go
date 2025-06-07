package main

import (
	"github.com/andy98725/elo-service/src/api"
	mm "github.com/andy98725/elo-service/src/matchmaking/service"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"

	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	e := echo.New()

	// Initialize middleware
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Logger.SetLevel(log.DEBUG)

	// Initialize server
	s, err := server.InitServer(e)
	if err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}
	defer close(s.Shutdown)

	// Migrate database
	if err = models.Migrate(); err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}

	// Initialize routes
	if err = api.InitRoutes(e); err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}

	// Start worker
	go mm.RunWorker(s.Shutdown)

	s.Start()
}
