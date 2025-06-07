package api

import (
	"com/everlastinggames/elo/src/api/game"
	"com/everlastinggames/elo/src/api/user"
	"net/http"

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

	return nil
}
