package game

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/games", CreateGame, auth.RequireAuth)
	e.GET("/games", GetGames, auth.RequireAdmin)
	e.GET("/games/:id", GetGame, auth.RequireAuth)
	e.GET("/user/games", GetGamesOfUser, auth.RequireAuth)

	return nil
}
