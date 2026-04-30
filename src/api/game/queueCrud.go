package game

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

// CreateGameQueueRequest mirrors models.CreateGameQueueParams as JSON.
// Defaults applied server-side via applyQueueDefaults — clients can omit
// any field except the ones they care about.
type CreateGameQueueRequest struct {
	Name                    string  `json:"name"`
	LobbyEnabled            *bool   `json:"lobby_enabled"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
	DefaultRating           int     `json:"default_rating"`
	KFactor                 int     `json:"k_factor"`
	MetadataEnabled         *bool   `json:"metadata_enabled"`
}

// requireGameOwner loads the parent game and verifies the caller owns it.
// All queue mutations gate through here. Admins are intentionally not
// granted bypass — game ownership is the only authority for queue config.
func requireGameOwner(ctx echo.Context, gameID string) (*models.Game, *models.User, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return nil, nil, echo.NewHTTPError(http.StatusNotFound, "Game not found")
	}
	user := ctx.Get("user").(*models.User)
	if game.OwnerID != user.ID {
		return nil, nil, echo.NewHTTPError(http.StatusForbidden, models.ErrNotGameOwner.Error())
	}
	return game, user, nil
}

// CreateGameQueue godoc
// @Summary      Create a queue under a game
// @Description  Adds a new matchmaking queue under an existing game. Owner-only. Queue names are unique within a game.
// @Tags         Games
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string                 true "Game UUID"
// @Param        body   body CreateGameQueueRequest true "Queue creation payload"
// @Success      200 {object} models.GameQueueResp
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      409 {object} echo.HTTPError "queue name already taken within this game"
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{gameID}/queue [post]
func CreateGameQueue(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID is required")
	}

	if _, _, err := requireGameOwner(ctx, gameID); err != nil {
		return err
	}

	req := new(CreateGameQueueRequest)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	queue, err := models.CreateGameQueue(gameID, models.CreateGameQueueParams{
		Name:                    req.Name,
		LobbyEnabled:            req.LobbyEnabled,
		LobbySize:               req.LobbySize,
		MatchmakingStrategy:     req.MatchmakingStrategy,
		MatchmakingMachineName:  req.MatchmakingMachineName,
		MatchmakingMachinePorts: req.MatchmakingMachinePorts,
		ELOStrategy:             req.ELOStrategy,
		DefaultRating:           req.DefaultRating,
		KFactor:                 req.KFactor,
		MetadataEnabled:         req.MetadataEnabled,
	})
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "queue name already taken within this game")
		}
		// Validation errors from applyQueueDefaults map to 400.
		if strings.HasPrefix(err.Error(), "invalid ") {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		slog.Error("Error creating game queue", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating queue")
	}

	return ctx.JSON(http.StatusOK, queue.ToResp())
}

// ListGameQueues godoc
// @Summary      List queues for a game
// @Description  Returns all queues for the game in canonical order (oldest first). Public — no auth required. The first element is the default queue used when no queueID is supplied to /match/join, /lobby/host, /user/rating, etc.
// @Tags         Games
// @Produce      json
// @Param        gameID path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "queues"
// @Failure      400 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Router       /game/{gameID}/queue [get]
func ListGameQueues(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	if gameID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID is required")
	}
	if _, err := models.GetGame(gameID); err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Game not found")
	}
	queues, err := models.GetGameQueuesForGame(gameID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error listing queues: "+err.Error())
	}
	out := make([]models.GameQueueResp, len(queues))
	for i, q := range queues {
		out[i] = *q.ToResp()
	}
	return ctx.JSON(http.StatusOK, echo.Map{"queues": out})
}

// GetGameQueue godoc
// @Summary      Get a single queue
// @Description  Returns one queue by ID. Public — no auth required.
// @Tags         Games
// @Produce      json
// @Param        gameID  path string true "Game UUID"
// @Param        queueID path string true "GameQueue UUID"
// @Success      200 {object} models.GameQueueResp
// @Failure      404 {object} echo.HTTPError
// @Router       /game/{gameID}/queue/{queueID} [get]
func GetGameQueue(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	queueID := ctx.Param("queueID")
	if gameID == "" || queueID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID and queueID are required")
	}
	queue, err := models.GetGameQueue(queueID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}
	if queue.GameID != gameID {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}
	return ctx.JSON(http.StatusOK, queue.ToResp())
}

// UpdateGameQueue godoc
// @Summary      Update a queue
// @Description  Updates queue settings. Game-owner only. Conditional update — only non-zero / non-nil fields in the payload are applied.
// @Tags         Games
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        gameID  path string                       true "Game UUID"
// @Param        queueID path string                       true "GameQueue UUID"
// @Param        body    body models.UpdateGameQueueParams true "Fields to update"
// @Success      200 {object} models.GameQueueResp
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      409 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{gameID}/queue/{queueID} [put]
func UpdateGameQueue(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	queueID := ctx.Param("queueID")
	if gameID == "" || queueID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID and queueID are required")
	}

	if _, _, err := requireGameOwner(ctx, gameID); err != nil {
		return err
	}

	existing, err := models.GetGameQueue(queueID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}
	if existing.GameID != gameID {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}

	req := new(models.UpdateGameQueueParams)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	queue, err := models.UpdateGameQueue(queueID, *req)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			return echo.NewHTTPError(http.StatusConflict, "queue name already taken within this game")
		}
		if strings.HasPrefix(err.Error(), "invalid ") {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "error updating queue: "+err.Error())
	}

	return ctx.JSON(http.StatusOK, queue.ToResp())
}

// DeleteGameQueue godoc
// @Summary      Delete a queue
// @Description  Deletes a queue. Game-owner only. Refuses to delete the only remaining queue for a game (409). Deleting the current default queue silently promotes the next-oldest to default.
// @Tags         Games
// @Produce      json
// @Security     BearerAuth
// @Param        gameID  path string true "Game UUID"
// @Param        queueID path string true "GameQueue UUID"
// @Success      200 {object} map[string]string "message"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      409 {object} echo.HTTPError "cannot delete the last remaining queue"
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{gameID}/queue/{queueID} [delete]
func DeleteGameQueue(ctx echo.Context) error {
	gameID := ctx.Param("gameID")
	queueID := ctx.Param("queueID")
	if gameID == "" || queueID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "gameID and queueID are required")
	}

	if _, _, err := requireGameOwner(ctx, gameID); err != nil {
		return err
	}

	existing, err := models.GetGameQueue(queueID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}
	if existing.GameID != gameID {
		return echo.NewHTTPError(http.StatusNotFound, "Queue not found")
	}

	if err := models.DeleteGameQueue(queueID); err != nil {
		if errors.Is(err, models.ErrLastQueue) {
			return echo.NewHTTPError(http.StatusConflict, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "error deleting queue: "+err.Error())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"message": "Queue deleted successfully"})
}
