package user

import (
	"errors"
	"net/http"

	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

type GuestRequest struct {
	DisplayName string `json:"displayName"`
}

func GuestToken(ctx echo.Context) error {
	req := new(GuestRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	if req.DisplayName == "" {
		return errors.New("missing required fields")
	}
	token, err := auth.GuestLogin(req.DisplayName)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"token": token,
	})
}
