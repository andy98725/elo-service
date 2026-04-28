package rating

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.GET("/user/rating/:gameId", GetRating, auth.RequireUserAuth)
	e.GET("/game/:gameId/leaderboard", GetLeaderboard)

	return nil
}
