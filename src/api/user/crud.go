package user

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

// GetUser godoc
// @Summary      Get current user
// @Description  Returns the currently authenticated user's profile
// @Tags         Users
// @Produce      json
// @Security     BearerAuth
// @Success      200 {object} models.UserResp
// @Failure      500 {object} echo.HTTPError
// @Router       /user [get]
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

// GetUsers godoc
// @Summary      List all users (admin)
// @Description  Returns a paginated list of all users. Admin only.
// @Tags         Users
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int false "Page number (default 0)"
// @Param        pageSize query int false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "users, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /users [get]
func GetUsers(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	users, nextPage, err := models.GetUsers(page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting users")
	}

	return ctx.JSON(200, echo.Map{
		"users":    users,
		"nextPage": nextPage,
	})
}

// UpdateUser godoc
// @Summary      Update current user
// @Description  Updates the authenticated user's profile (not yet implemented)
// @Tags         Users
// @Security     BearerAuth
// @Success      200
// @Router       /user [put]
func UpdateUser(ctx echo.Context) error {
	//TODO
	return nil
}

// DeleteUser godoc
// @Summary      Delete current user
// @Description  Deletes the authenticated user's account (not yet implemented)
// @Tags         Users
// @Security     BearerAuth
// @Success      200
// @Router       /user [delete]
func DeleteUser(ctx echo.Context) error {
	//TODO
	return nil
}
