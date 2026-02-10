package matchResults

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/api/middleware"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Match reporting
	e.POST("/result/report", ReportResults, middleware.AllowCors())
	e.GET("/results/:matchID/logs", GetMatchLogs, auth.RequireUserOrGuestAuth)

	// CRUD
	e.GET("/results/:matchID", GetMatchResult, auth.RequireUserOrGuestAuth)
	e.GET("/game/:gameID/results", GetMatchResultsOfGame, auth.RequireUserOrGuestAuth)
	e.GET("/user/results", GetMatchResultsOfCurrentUser, auth.RequireUserOrGuestAuth)

	// Admin only
	e.GET("/results", GetMatchResults, auth.RequireAdmin)
	e.GET("/user/:userID/results", GetMatchResultsOfUser, auth.RequireAdmin)
	return nil
}
