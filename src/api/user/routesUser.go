package user

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/api/middleware"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/guest/login", GuestToken, middleware.AllowCors())
	e.POST("/user", Register, middleware.AllowCors())
	e.POST("/user/login", Login, middleware.AllowCors())
	e.GET("/user", GetUser, auth.RequireUserAuth)
	e.GET("/users", GetUsers, auth.RequireAdmin)
	e.PUT("/user", UpdateUser, auth.RequireUserAuth)
	e.DELETE("/user", DeleteUser, auth.RequireUserAuth)

	return nil
}
