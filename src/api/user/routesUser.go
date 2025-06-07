package user

import (
	"github.com/andy98725/elo-service/src/api/auth"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/user", Register)
	e.POST("/user/login", Login)
	e.GET("/user", GetUser, auth.RequireAuth)
	e.GET("/users", GetUsers, auth.RequireAdmin)
	e.PUT("/user", UpdateUser, auth.RequireAuth)
	e.DELETE("/user", DeleteUser, auth.RequireAuth)

	return nil
}
