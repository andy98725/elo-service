package rating

import (
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

// GetRating godoc
// @Summary      Get user rating for a game queue
// @Description  Returns the authenticated user's rating for the given game's queue. A row is lazy-created at the queue's DefaultRating on first access. Defaults to the game's primary queue when queueID is omitted.
// @Tags         Ratings
// @Produce      json
// @Security     BearerAuth
// @Param        gameId  path  string true  "Game UUID"
// @Param        queueID query string false "Specific GameQueue UUID (defaults to primary queue)"
// @Success      200 {object} map[string]interface{} "player_id, game_queue_id, rating"
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

	queue, err := models.ResolveQueue(gameID, ctx.QueryParam("queueID"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "queue not found: "+err.Error())
	}

	rating, err := models.GetRating(userID, queue.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "rating not found")
	}

	return ctx.JSON(http.StatusOK, echo.Map{
		"player_id":     rating.PlayerID,
		"game_queue_id": rating.GameQueueID,
		"rating":        rating.Rating,
	})
}

// GetLeaderboard godoc
// @Summary      Game queue leaderboard
// @Description  Returns the top-rated players for a game queue, paginated. Ordered by rating descending. Public — no auth required. Defaults to the game's primary queue when queueID is omitted.
// @Tags         Ratings
// @Produce      json
// @Param        gameId   path  string true  "Game UUID"
// @Param        queueID  query string false "Specific GameQueue UUID (defaults to primary queue)"
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

	queue, err := models.ResolveQueue(gameID, ctx.QueryParam("queueID"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "queue not found: "+err.Error())
	}

	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	ratings, nextPage, err := models.GetLeaderboard(queue.ID, page, pageSize)
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
		"leaderboard":   out,
		"nextPage":      nextPage,
		"game_queue_id": queue.ID,
	})
}
