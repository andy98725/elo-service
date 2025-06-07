package user

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register creates the user with the provided username, email, and password.
func Register(ctx echo.Context) error {
	req := new(RegisterRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		return errors.New("missing required fields")
	}

	user, err := models.CreateUser(models.CreateUserParams{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "duplicate key value violates unique constraint") {
			if strings.Contains(errMsg, "username") {
				return echo.NewHTTPError(http.StatusBadRequest, "username already taken")
			}
			if strings.Contains(errMsg, "email") {
				return echo.NewHTTPError(http.StatusBadRequest, "email already registered")
			}
			return echo.NewHTTPError(http.StatusBadRequest, "user already exists")
		}

		slog.Error("Error creating user", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating user")
	}

	return ctx.JSON(200, user.ToResp())
}
