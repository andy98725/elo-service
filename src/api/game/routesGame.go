package game

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/game", CreateGame, auth.RequireAuth)
	e.GET("/game", GetGames, auth.RequireAdmin)
	e.GET("/game/:id", GetGame, auth.RequireAuth)
	e.GET("/user/game", GetGamesOfUser, auth.RequireAuth)

	return nil
}
