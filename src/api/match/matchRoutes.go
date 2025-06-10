package match

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Matchmaking
	e.GET("/match/join", JoinQueueWebsocket, auth.RequireAuth)
	// CRUD
	e.GET("/match/:matchID", GetMatch, auth.RequireAuth)
	e.GET("/match/game/:gameID", GetMatchesOfGame, auth.RequireAuth)
	e.GET("/match", GetMatches, auth.RequireAuth)
	e.GET("/result/:matchID", GetMatchResult, auth.RequireAuth)
	e.GET("/result/game/:gameID", GetMatchResultsOfGame, auth.RequireAuth)
	e.GET("/result", GetMatchResults, auth.RequireAuth)
	return nil
}
