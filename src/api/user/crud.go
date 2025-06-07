package user

import (
	"net/http"
	"strconv"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

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
