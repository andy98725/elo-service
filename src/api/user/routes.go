package user

import (
	"github.com/andy98725/elo-service/src/api/auth"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/users/register", RegisterUser)
	e.POST("/users/login", Login)

	e.POST("/users/new", CreateUser)
	e.GET("/users/get", GetUser, auth.RequireAuth)
	e.GET("/users", GetUsers, auth.RequireAdmin)
	e.POST("/users/update", UpdateUser, auth.RequireAuth)
	e.DELETE("/users/delete", DeleteUser, auth.RequireAuth)

	return nil
}
