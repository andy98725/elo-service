package matchResults

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// GetMatchResult godoc
// @Summary      Get a match result
// @Description  Returns the result of a completed match
// @Tags         Results
// @Produce      json
// @Security     BearerAuth
// @Param        matchID path string true "Match result UUID"
// @Success      200 {object} models.MatchResultResp
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /results/{matchID} [get]
func GetMatchResult(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	id := ctx.Get("id").(string)

	matchResult, err := models.GetMatchResult(matchID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if canSee, err := models.CanUserSeeMatchResult(id, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	}

	return ctx.JSON(http.StatusOK, matchResult.ToResp())
}

// GetMatchResultsOfGame godoc
// @Summary      Get match results for a game
// @Description  Returns a paginated list of match results for a specific game
// @Tags         Results
// @Produce      json
// @Security     BearerAuth
// @Param        gameID   path  string true "Game UUID"
// @Param        page     query int    false "Page number (default 0)"
// @Param        pageSize query int    false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matchResults, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{gameID}/results [get]
func GetMatchResultsOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	id := ctx.Get("id").(string)
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	matchResults, nextPage, err := models.GetMatchResultsOfGame(gameID, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	matchResultsResp := []models.MatchResultResp{}
	for _, matchResult := range matchResults {
		if canSee, err := models.CanUserSeeMatchResult(id, matchResult.ID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
		} else if !canSee {
			continue
		}
		matchResultsResp = append(matchResultsResp, *matchResult.ToResp())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

// GetMatchResultsOfCurrentUser godoc
// @Summary      Get current user's match results
// @Description  Returns a paginated list of match results for the authenticated user
// @Tags         Results
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int false "Page number (default 0)"
// @Param        pageSize query int false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matchResults, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /user/results [get]
func GetMatchResultsOfCurrentUser(ctx echo.Context) error {
	id := ctx.Get("id").(string)
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	matchResults, nextPage, err := models.GetMatchResultsOfPlayer(id, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

// GetMatchResults godoc
// @Summary      List all match results (admin)
// @Description  Returns a paginated list of all match results. Admin only.
// @Tags         Results
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int false "Page number (default 0)"
// @Param        pageSize query int false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matchResults, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /results [get]
func GetMatchResults(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	matchResults, nextPage, err := models.GetMatchResults(page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}

// GetMatchResultsOfUser godoc
// @Summary      Get match results for a user (admin)
// @Description  Returns a paginated list of match results for a specific user. Admin only.
// @Tags         Results
// @Produce      json
// @Security     BearerAuth
// @Param        userID   path  string true "User UUID"
// @Param        page     query int    false "Page number (default 0)"
// @Param        pageSize query int    false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matchResults, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /user/{userID}/results [get]
func GetMatchResultsOfUser(ctx echo.Context) error {
	id := ctx.Param("userID")
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	matchResults, nextPage, err := models.GetMatchResultsOfPlayer(id, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	matchResultsResp := make([]models.MatchResultResp, len(matchResults))
	for i, matchResult := range matchResults {
		matchResultsResp[i] = *matchResult.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matchResults": matchResultsResp, "nextPage": nextPage})
}
