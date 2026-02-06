package rating

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Create routes for Rating
	e.GET("/user/rating/:gameId", GetRating, auth.RequireUserAuth)
	// e.POST("/rating", CreateRating)
	// e.PUT("/rating/:id", UpdateRating)
	// e.DELETE("/rating/:id", DeleteRating)

	return nil
}
