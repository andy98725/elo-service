package rating

import (
	"github.com/labstack/echo"
)

// GetRating godoc
// @Summary      Get user rating for a game
// @Description  Returns the current user's ELO rating for a specific game (not yet implemented)
// @Tags         Ratings
// @Produce      json
// @Security     BearerAuth
// @Param        gameId path string true "Game UUID"
// @Success      200 {object} map[string]string "message"
// @Router       /user/rating/{gameId} [get]
func GetRating(ctx echo.Context) error {
	// gameID := ctx.Param("gameId")
	// userID, err := models.UserIDFromContext(ctx)
	// if err != nil {
	// 	return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	// }

	// return ctx.JSON(200, user.ToResp())
	// TODO
	return ctx.JSON(200, echo.Map{"message": "TODO"})
}
