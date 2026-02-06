package match

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

func GetMatch(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	match, err := models.GetMatch(matchID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "Match not found")
	} else if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	if canSee, err := models.CanUserSeeMatch(userID, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match not found")
	}

	return ctx.JSON(http.StatusOK, match.ToResp())
}

func GetMatchesOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	matches, nextPage, err := models.GetMatchesOfGame(gameID, page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchesResp := []models.MatchResp{}
	for _, match := range matches {
		if canSee, err := models.CanUserSeeMatch(userID, match.ID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match: "+err.Error())
		} else if !canSee {
			continue
		}
		matchesResp = append(matchesResp, *match.ToResp())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": matchesResp, "nextPage": nextPage})
}

// Admin only
func GetMatches(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matches, nextPage, err := models.GetMatches(page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": matches, "nextPage": nextPage})
}
