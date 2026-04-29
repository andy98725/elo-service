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
	// Username and Email are self-service. Pointers distinguish "omitted"
	// from "explicit empty string" so callers can change one without
	// touching the other.
	Username *string `json:"username,omitempty"`
	Email    *string `json:"email,omitempty"`
	// CanCreateGame is admin-only. Non-admins setting it (either value) get a 403.
	CanCreateGame *bool `json:"can_create_game,omitempty"`
}

// UpdateUser godoc
// @Summary      Update a user
// @Description  Updates the authenticated user's username and/or email. Admins may target another user with `?id=<userID>` and may also flip `can_create_game`. Email changes are trusted from the client today; a verification round-trip is planned before any feature relies on email identity.
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
// @Failure      409 {object} echo.HTTPError "username or email already taken"
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

	if req.Username != nil && *req.Username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username cannot be empty")
	}
	if req.Email != nil && *req.Email == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "email cannot be empty")
	}

	if req.Username != nil || req.Email != nil {
		target, err = models.UpdateUserProfile(targetID, req.Username, req.Email)
		if err != nil {
			switch {
			case errors.Is(err, models.ErrUsernameTaken):
				return echo.NewHTTPError(http.StatusConflict, err.Error())
			case errors.Is(err, models.ErrEmailTaken):
				return echo.NewHTTPError(http.StatusConflict, err.Error())
			}
			return echo.NewHTTPError(http.StatusInternalServerError, "error updating user: "+err.Error())
		}
	}

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

type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword godoc
// @Summary      Change own password
// @Description  Rotates the authenticated user's password. Requires the current password — admin impersonation does NOT bypass this check, since impersonation is for support actions, not credential rotation.
// @Tags         Users
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body ChangePasswordRequest true "current_password, new_password"
// @Success      200 {object} map[string]string "status"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError "current password did not match"
// @Router       /user/password [put]
func ChangePassword(ctx echo.Context) error {
	req := new(ChangePasswordRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "current_password and new_password are required")
	}

	// Always operate on the actor, never the impersonation target. Password
	// rotation is a credential change; an admin shouldn't silently rotate
	// another user's password through the impersonation channel.
	actor := ctx.Get("user").(*models.User)

	if err := models.ChangePassword(actor.ID, req.CurrentPassword, req.NewPassword); err != nil {
		if errors.Is(err, models.ErrInvalidPassword) {
			return echo.NewHTTPError(http.StatusUnauthorized, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "error changing password: "+err.Error())
	}
	return ctx.JSON(http.StatusOK, echo.Map{"status": "ok"})
}

// DeleteUser godoc
// @Summary      Delete own account (soft delete)
// @Description  Soft-deletes the authenticated user. The row stays in the database — match history and ratings keep their FKs intact — but the account can no longer log in and is hidden from listings. Username and email continue to occupy their unique-index slot, so they are NOT released for re-registration. Admins can target another user with `?id=<userID>`.
// @Tags         Users
// @Security     BearerAuth
// @Param        id query string false "Target user UUID (admin only)"
// @Success      200 {object} map[string]string "status"
// @Failure      404 {object} echo.HTTPError
// @Router       /user [delete]
func DeleteUser(ctx echo.Context) error {
	targetID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error resolving user: "+err.Error())
	}
	if _, err := models.GetById(targetID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "user not found")
	}
	if err := models.SoftDeleteUser(targetID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error deleting user: "+err.Error())
	}
	return ctx.JSON(http.StatusOK, echo.Map{"status": "ok"})
}
