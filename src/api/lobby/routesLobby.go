package lobby

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.GET("/lobby/host", HostLobby, auth.RequireUserOrGuestAuth)
	e.GET("/lobby/find", FindLobby, auth.RequireUserOrGuestAuth)
	e.GET("/lobby/join", JoinLobby, auth.RequireUserOrGuestAuth)

	return nil
}
