package user

import (
	"com/everlastinggames/elo/src/api/auth"
	"com/everlastinggames/elo/src/server"
	"net/http"

	"github.com/labstack/echo"
)

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func Login(c echo.Context) error {
	req := new(LoginRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	token, err := auth.Login(req.Username, req.Password)
	if err != nil {
		server.S.Logger.Error("Login failed", "error", err)
		return echo.NewHTTPError(http.StatusUnauthorized, "Invalid credentials")
	}

	return c.JSON(http.StatusOK, map[string]string{
		"token": token,
	})
}
