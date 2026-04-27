package match

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// GetMatch godoc
// @Summary      Get a match by ID
// @Description  Returns match details. User must be a participant or game owner.
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        matchID path string true "Match UUID"
// @Success      200 {object} models.MatchResp
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /match/{matchID} [get]
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
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if canSee, err := models.CanUserSeeMatch(userID, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match not found")
	}

	return ctx.JSON(http.StatusOK, match.ToResp())
}

// GetMatchesOfGame godoc
// @Summary      Get matches for a game
// @Description  Returns a paginated list of matches for a specific game
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        gameID   path  string true "Game UUID"
// @Param        page     query int    false "Page number (default 0)"
// @Param        pageSize query int    false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matches, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /match/game/{gameID} [get]
func GetMatchesOfGame(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	matches, nextPage, err := models.GetMatchesOfGame(gameID, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
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

// GetMatches godoc
// @Summary      List all matches (admin)
// @Description  Returns a paginated list of all matches. Admin only.
// @Tags         Matches
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int false "Page number (default 0)"
// @Param        pageSize query int false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "matches, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /matches [get]
func GetMatches(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	matches, nextPage, err := models.GetMatches(page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"matches": matches, "nextPage": nextPage})
}
