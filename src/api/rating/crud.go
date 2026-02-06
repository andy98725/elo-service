package rating

import (
	"github.com/labstack/echo"
)

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
