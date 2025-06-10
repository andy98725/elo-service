package user

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
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
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	users, nextPage, err := models.GetUsers(page, pageSize)
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
