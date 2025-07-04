package user

import (
	"net/http"

	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

type LoginRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

func Login(c echo.Context) error {
	req := new(LoginRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	token, user, err := auth.Login(req.Email, req.DisplayName, req.Password)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	}

	return c.JSON(http.StatusOK, map[string]string{
		"token":       token,
		"displayName": req.DisplayName,
		"id":          user.ID,
	})
}
