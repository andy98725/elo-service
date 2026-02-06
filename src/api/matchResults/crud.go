package matchResults

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

func GetMatchResult(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	matchResult, err := models.GetMatchResult(matchID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	} else if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	if canSee, err := models.CanUserSeeMatchResult(userID, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	}

	return ctx.JSON(http.StatusOK, matchResult.ToResp())
}

func GetMatchResultsOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matchResults, nextPage, err := models.GetMatchResultsOfGame(gameID, page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchResultsResp := []models.MatchResultResp{}
	for _, matchResult := range matchResults {
		if canSee, err := models.CanUserSeeMatchResult(userID, matchResult.ID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
		} else if !canSee {
			continue
		}
		matchResultsResp = append(matchResultsResp, *matchResult.ToResp())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

func GetMatchResultsOfUser(ctx echo.Context) error {
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matchResults, nextPage, err := models.GetMatchResultsOfPlayer(userID, page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

// Admin only
func GetMatchResults(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matchResults, nextPage, err := models.GetMatchResults(page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}
