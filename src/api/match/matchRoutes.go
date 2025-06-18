package match

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/api/match/results"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Matchmaking
	e.GET("/match/join", JoinQueueWebsocket, auth.RequireUserOrGuestAuth)
	// CRUD
	e.GET("/match/:matchID", GetMatch, auth.RequireUserAuth)
	e.GET("/match/game/:gameID", GetMatchesOfGame, auth.RequireUserAuth)
	e.GET("/match", GetMatches, auth.RequireUserAuth)
	e.GET("/result/:matchID", GetMatchResult, auth.RequireUserAuth)
	e.GET("/result/game/:gameID", GetMatchResultsOfGame, auth.RequireUserAuth)
	e.GET("/result", GetMatchResults, auth.RequireUserAuth)

	// Report results
	e.POST("/result/report", results.ReportResults)
	return nil
}
