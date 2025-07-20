package user

import (
	"github.com/andy98725/elo-service/src/api/auth"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/guest/login", GuestToken, auth.DisableCors)
	e.POST("/user", Register, auth.DisableCors)
	e.POST("/user/login", Login, auth.DisableCors)
	e.GET("/user", GetUser, auth.RequireUserAuth)
	e.GET("/users", GetUsers, auth.RequireAdmin)
	e.PUT("/user", UpdateUser, auth.RequireUserAuth)
	e.DELETE("/user", DeleteUser, auth.RequireUserAuth)

	return nil
}
