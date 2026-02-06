package match

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/api/middleware"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Matchmaking
	e.GET("/match/join", JoinQueueWebsocket, auth.RequireUserOrGuestAuth, middleware.AllowCors())
	e.GET("/match/size", QueueSize, auth.RequireUserOrGuestAuth, middleware.AllowCors())

	// CRUD
	e.GET("/match/:matchID", GetMatch, auth.RequireUserAuth)
	e.GET("/match/game/:gameID", GetMatchesOfGame, auth.RequireUserAuth)
	e.GET("/matches", GetMatches, auth.RequireAdmin)

	return nil
}
