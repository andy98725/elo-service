package match

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/match/join", JoinQueue, auth.RequireAuth)
	return nil
}
