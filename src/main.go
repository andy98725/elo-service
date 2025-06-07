package main

import (
	"github.com/andy98725/elo-service/src/api"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"

	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	e := echo.New()

	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Logger.SetLevel(log.DEBUG)

	s, err := server.InitServer(e)
	if err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}

	if err = models.Migrate(); err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}

	if err = api.InitRoutes(e); err != nil {
		e.Logger.Fatal(err)
		panic(err)
	}

	s.Start()
}
