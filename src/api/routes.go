package api

import (
	"net/http"

	"github.com/andy98725/elo-service/src/api/game"
	"github.com/andy98725/elo-service/src/api/match"
	"github.com/andy98725/elo-service/src/api/matchResults"
	"github.com/andy98725/elo-service/src/api/rating"
	"github.com/andy98725/elo-service/src/api/user"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, struct {
			Status string `json:"status"`
		}{Status: "healthy!"})
	})

	if err := user.InitRoutes(e); err != nil {
		return err
	}
	if err := game.InitRoutes(e); err != nil {
		return err
	}
	if err := match.InitRoutes(e); err != nil {
		return err
	}
	if err := matchResults.InitRoutes(e); err != nil {
		return err
	}
	if err := rating.InitRoutes(e); err != nil {
		return err
	}

	return nil
}
