package match

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

func GetMatch(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	match, err := models.GetMatch(matchID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	return ctx.JSON(http.StatusOK, match.ToResp())
}

func GetMatchesOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matches, nextPage, err := models.GetMatchesOfGame(gameID, page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchesResp := make([]models.MatchResp, len(matches))
	for i, match := range matches {
		matchesResp[i] = *match.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": matchesResp, "nextPage": nextPage})
}

func GetMatches(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matches, nextPage, err := models.GetMatches(page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchesResp := make([]models.MatchResp, len(matches))
	for i, match := range matches {
		matchesResp[i] = *match.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": matchesResp, "nextPage": nextPage})
}

func GetMatchResult(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	matchResult, err := models.GetMatchResult(matchID)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}
	return ctx.JSON(http.StatusOK, matchResult.ToResp())
}

func GetMatchResultsOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	matchResults, nextPage, err := models.GetMatchResultsOfGame(gameID, page, pageSize)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, echo.Map{"error": err.Error()})
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

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
