package game

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/game", CreateGame, auth.RequireUserAuth)
	e.GET("/games", GetGames, auth.RequireAdmin)
	e.GET("/game/:id", GetGame, auth.RequireUserAuth)
	e.GET("/user/game", GetGamesOfUser, auth.RequireUserAuth)

	return nil
}
