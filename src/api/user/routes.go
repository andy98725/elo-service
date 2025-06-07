package user

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"

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

		server.S.Logger.Error("Error creating user", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating user")
	}

	return ctx.JSON(200, user.ToResp())
}

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func Login(c echo.Context) error {
	req := new(LoginRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	token, err := auth.Login(req.Email, req.Password)
	if err != nil {
		return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
	}

	return c.JSON(http.StatusOK, map[string]string{
		"token": token,
	})
}

func GetUser(ctx echo.Context) error {
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	user, err := models.GetById(userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	return ctx.JSON(200, user.ToResp())
}

func GetUsers(ctx echo.Context) error {
	page := ctx.QueryParam("page")
	pageSize := ctx.QueryParam("pageSize")
	if page == "" {
		page = "0"
	}
	if pageSize == "" {
		pageSize = "10"
	}

	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid page param")
	}
	pageSizeInt, err := strconv.Atoi(pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid pageSize param")
	}

	users, nextPage, err := models.GetUsers(pageInt, pageSizeInt)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting users")
	}

	return ctx.JSON(200, struct {
		Users    []models.User `json:"users"`
		NextPage int           `json:"nextPage"`
	}{
		Users:    users,
		NextPage: nextPage,
	})
}

// UpdateUser updates the user's username, email, and password
// matching the provided ID.
// Requires admin.
// Returns the user struct.
func UpdateUser(ctx echo.Context) error {
	//TODO
	return nil
}

func DeleteUser(ctx echo.Context) error {
	//TODO
	return nil
}
