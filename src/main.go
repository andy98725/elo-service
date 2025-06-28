package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

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
	go mm.RunWorker(context.Background(), s.Shutdown)
	// Start server
	go e.Start(":" + s.Config.Port)

	// Wait for interrupt signal to gracefully shutdown the server
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	<-quit
	e.Logger.Info("Shutting down server...")
	close(s.Shutdown)

	// Give components time to clean up
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Fatal(err)
	}
}
