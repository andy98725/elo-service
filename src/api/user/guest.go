package user

import (
	"net/http"

	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

type GuestRequest struct {
	DisplayName string `json:"displayName"`
}

// GuestToken godoc
// @Summary      Create a guest session
// @Description  Returns a JWT token for a guest user with the given display name
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body body GuestRequest true "Guest login payload"
// @Success      200 {object} map[string]string "token, displayName, id"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Router       /guest/login [post]
func GuestToken(ctx echo.Context) error {
	req := new(GuestRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	if req.DisplayName == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "displayName is required")
	}
	if util.IsProfane(req.DisplayName) {
		return echo.NewHTTPError(http.StatusBadRequest, "displayName contains disallowed language")
	}
	token, claims, err := auth.GuestLogin(req.DisplayName)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	}

	return ctx.JSON(http.StatusOK, map[string]string{
		"token":       token,
		"displayName": claims.DisplayName,
		"id":          claims.ID,
	})
}
