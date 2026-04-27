package user

import (
	"net/http"

	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

type LoginRequest struct {
	Email       string `json:"email"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
}

// Login godoc
// @Summary      Log in a user
// @Description  Authenticates a user with email and password, returns a JWT token
// @Tags         Auth
// @Accept       json
// @Produce      json
// @Param        body body LoginRequest true "Login payload"
// @Success      200 {object} map[string]string "token, displayName, id"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Router       /user/login [post]
func Login(c echo.Context) error {
	req := new(LoginRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	if req.DisplayName != "" && util.IsProfane(req.DisplayName) {
		return echo.NewHTTPError(http.StatusBadRequest, "displayName contains disallowed language")
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
