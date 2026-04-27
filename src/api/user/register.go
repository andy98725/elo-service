package user

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}

type RegisterRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Register godoc
// @Summary      Register a new user
// @Description  Creates a user with the provided username, email, and password
// @Tags         Users
// @Accept       json
// @Produce      json
// @Param        body body RegisterRequest true "Registration payload"
// @Success      200 {object} models.UserResp
// @Failure      400 {object} echo.HTTPError
// @Failure      409 {object} echo.HTTPError "username or email already taken"
// @Failure      500 {object} echo.HTTPError
// @Router       /user [post]
func Register(ctx echo.Context) error {
	req := new(RegisterRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	if req.Username == "" || req.Email == "" || req.Password == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username, email, and password are required")
	}

	user, err := models.CreateUser(models.CreateUserParams{
		Username: req.Username,
		Email:    req.Email,
		Password: req.Password,
	})
	if err != nil {
		if isUniqueConstraintViolation(err) {
			errMsg := err.Error()
			if strings.Contains(errMsg, "username") {
				return echo.NewHTTPError(http.StatusConflict, "username already taken")
			}
			if strings.Contains(errMsg, "email") {
				return echo.NewHTTPError(http.StatusConflict, "email already registered")
			}
			return echo.NewHTTPError(http.StatusConflict, "user already exists")
		}

		slog.Error("Error creating user", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating user")
	}

	return ctx.JSON(200, user.ToResp())
}
