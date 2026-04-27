package user

import (
	"errors"
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
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
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

type UpdateUserRequest struct {
	// CanCreateGame is admin-only. Pointer so we can distinguish "field
	// omitted" from "explicit false" — non-admins setting it (either value)
	// gets a 403.
	CanCreateGame *bool `json:"can_create_game,omitempty"`
}

// UpdateUser godoc
// @Summary      Update a user
// @Description  Updates user fields. Defaults to the authenticated user;
// @Description  admins may target another user with `?id=<userID>`.
// @Description  `can_create_game` is admin-only.
// @Tags         Users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id   query string             false "Target user UUID (admin only)"
// @Param        body body UpdateUserRequest   true  "Fields to update"
// @Success      200 {object} models.UserResp
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Router       /user [put]
func UpdateUser(ctx echo.Context) error {
	req := new(UpdateUserRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	targetID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error resolving user: "+err.Error())
	}

	target, err := models.GetById(targetID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}

	actor := ctx.Get("user").(*models.User)

	if req.CanCreateGame != nil {
		target, err = models.SetCanCreateGame(targetID, *req.CanCreateGame, actor)
		if err != nil {
			if errors.Is(err, models.ErrNotAdmin) {
				return echo.NewHTTPError(http.StatusForbidden, err.Error())
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "error updating user: "+err.Error())
		}
	}

	return ctx.JSON(http.StatusOK, target.ToResp())
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
