package user

import (
	"com/everlastinggames/elo/src/api/auth"

	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	e.POST("/users/register", RegisterUser)
	e.POST("/users/login", Login)

	e.POST("/users/new", CreateUser, auth.RequireAdmin)
	e.POST("/users/get", GetUser, auth.RequireAdmin)
	e.POST("/users", GetUsers, auth.RequireAdmin)
	e.POST("/users/update", UpdateUser, auth.RequireAdmin)
	e.POST("/users/delete", DeleteUser, auth.RequireAdmin)

	return nil
}
