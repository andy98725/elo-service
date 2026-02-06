package matchResults

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/api/middleware"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Match reporting
	e.POST("/result/report", ReportResults, middleware.AllowCors())
	e.GET("/results/:matchID/logs", GetMatchLogs, auth.RequireUserAuth)

	// CRUD
	// TODO: Allow guests to access these routes
	e.GET("/results/:matchID", GetMatchResult, auth.RequireUserAuth)
	e.GET("/game/:gameID/results", GetMatchResultsOfGame, auth.RequireUserAuth)
	e.GET("/user/results", GetMatchResultsOfUser, auth.RequireUserAuth)
	e.GET("/results", GetMatchResults, auth.RequireAdmin)
	return nil
}
