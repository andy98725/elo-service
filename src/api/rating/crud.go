package rating

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

// GetRating godoc
// @Summary      Get user rating for a game
// @Description  Returns the authenticated user's rating for the given game. A row is lazy-created at the game's DefaultRating on first access.
// @Tags         Ratings
// @Produce      json
// @Security     BearerAuth
// @Param        gameId path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "player_id, game_id, rating"
// @Failure      400 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /user/rating/{gameId} [get]
func GetRating(ctx echo.Context) error {
	gameID := ctx.Param("gameId")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameId is required")
	}
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	rating, err := models.GetRating(userID, gameID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "rating not found")
	}

	return ctx.JSON(http.StatusOK, echo.Map{
		"player_id": rating.PlayerID,
		"game_id":   rating.GameID,
		"rating":    rating.Rating,
	})
}

// GetLeaderboard godoc
// @Summary      Game leaderboard
// @Description  Returns the top-rated players for a game, paginated. Ordered by rating descending. Public — no auth required.
// @Tags         Ratings
// @Produce      json
// @Param        gameId   path  string true  "Game UUID"
// @Param        page     query int    false "Page number (default 0)"
// @Param        pageSize query int    false "Page size (default 10, max 100)"
// @Success      200 {object} map[string]interface{} "leaderboard, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{gameId}/leaderboard [get]
func GetLeaderboard(ctx echo.Context) error {
	gameID := ctx.Param("gameId")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameId is required")
	}

	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ratings, nextPage, err := models.GetLeaderboard(gameID, page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting leaderboard: "+err.Error())
	}

	type entry struct {
		PlayerID string `json:"player_id"`
		Username string `json:"username"`
		Rating   int    `json:"rating"`
	}
	out := make([]entry, len(ratings))
	for i, r := range ratings {
		out[i] = entry{
			PlayerID: r.PlayerID,
			Username: r.Player.Username,
			Rating:   r.Rating,
		}
	}

	return ctx.JSON(http.StatusOK, echo.Map{
		"leaderboard": out,
		"nextPage":    nextPage,
	})
}
