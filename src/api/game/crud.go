package game

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

// isUniqueConstraintViolation reports whether err looks like a uniqueness
// violation across the supported drivers (Postgres in production, SQLite in
// integration tests). The two drivers return very different message formats.
func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key value violates unique constraint") ||
		strings.Contains(msg, "UNIQUE constraint failed")
}

type CreateGameRequest struct {
	Name                    string  `json:"name"`
	Description             string  `json:"description"`
	GuestsAllowed           bool    `json:"guests_allowed"`
	LobbyEnabled            *bool   `json:"lobby_enabled"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
	MetadataEnabled         bool    `json:"metadata_enabled"`
	PublicResults           *bool   `json:"public_results"`
	PublicMatchLogs         *bool   `json:"public_match_logs"`
}

// CreateGame godoc
// @Summary      Create a game
// @Description  Creates a new game with matchmaking and ELO configuration
// @Tags         Games
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        body body CreateGameRequest true "Game creation payload"
// @Success      200 {object} models.GameResp
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError "user is not allowed to create games"
// @Failure      409 {object} echo.HTTPError "game name already taken"
// @Failure      500 {object} echo.HTTPError
// @Router       /game [post]
func CreateGame(ctx echo.Context) error {
	req := new(CreateGameRequest)
	if err := ctx.Bind(req); err != nil {
		slog.Warn("Error binding request", "error", err)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}
	slog.Info("Creating game", "game", req)

	user := ctx.Get("user").(*models.User)
	if !user.IsAdmin && !user.CanCreateGame {
		return echo.NewHTTPError(http.StatusForbidden, "user is not allowed to create games")
	}
	game, err := models.CreateGame(models.CreateGameParams{
		Name:                    req.Name,
		Description:             req.Description,
		GuestsAllowed:           req.GuestsAllowed,
		LobbyEnabled:            req.LobbyEnabled,
		LobbySize:               req.LobbySize,
		MatchmakingStrategy:     req.MatchmakingStrategy,
		MatchmakingMachineName:  req.MatchmakingMachineName,
		MatchmakingMachinePorts: req.MatchmakingMachinePorts,
		ELOStrategy:             req.ELOStrategy,
		MetadataEnabled:         req.MetadataEnabled,
		PublicResults:           req.PublicResults,
		PublicMatchLogs:         req.PublicMatchLogs,
	}, *user)
	if err != nil {
		if isUniqueConstraintViolation(err) {
			if strings.Contains(err.Error(), "name") {
				return echo.NewHTTPError(http.StatusConflict, "game name already taken")
			}
			return echo.NewHTTPError(http.StatusConflict, "game already exists")
		}

		slog.Error("Error creating game", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating game")
	}

	return ctx.JSON(http.StatusOK, game.ToResp())
}

// GetGames godoc
// @Summary      List all games (admin)
// @Description  Returns a paginated list of all games. Admin only.
// @Tags         Games
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int false "Page number (default 0)"
// @Param        pageSize query int false "Page size (default 10)"
// @Success      200 {object} map[string]interface{} "games, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games [get]
func GetGames(ctx echo.Context) error {
	page, pageSize, err := util.ParsePagination(ctx)
	if err != nil {
		return ctx.JSON(http.StatusBadRequest, echo.Map{"error": err.Error()})
	}

	games, nextPage, err := models.GetGames(page, pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Error getting games: "+err.Error())
	}
	gamesResp := make([]models.GameResp, len(games))
	for i, game := range games {
		gamesResp[i] = *game.ToResp()
	}

	return ctx.JSON(http.StatusOK, echo.Map{
		"games":    gamesResp,
		"nextPage": nextPage,
	})
}

// GetGame godoc
// @Summary      Get a game by ID
// @Description  Returns a single game by its UUID
// @Tags         Games
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Game UUID"
// @Success      200 {object} models.GameResp
// @Failure      400 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Router       /game/{id} [get]
func GetGame(ctx echo.Context) error {
	id := ctx.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Game ID is required")
	}

	var game models.Game
	result := server.S.DB.First(&game, "id = ?", id)
	if result.Error != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Game not found")
	}

	return ctx.JSON(http.StatusOK, game.ToResp())
}

// GetGamesOfUser godoc
// @Summary      Get games owned by current user
// @Description  Returns a paginated list of games owned by the authenticated user
// @Tags         Games
// @Produce      json
// @Security     BearerAuth
// @Param        page     query int true "Page number"
// @Param        pageSize query int true "Page size"
// @Success      200 {object} map[string]interface{} "games, nextPage"
// @Failure      400 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /user/game [get]
func GetGamesOfUser(ctx echo.Context) error {
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	page := ctx.QueryParam("page")
	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid page param")
	}

	pageSize := ctx.QueryParam("pageSize")
	pageSizeInt, err := strconv.Atoi(pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid pageSize param")
	}

	games, nextPage, err := models.GetGamesOfUser(pageInt, pageSizeInt, userID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Error getting user games: "+err.Error())
	}
	gamesResp := make([]models.GameResp, len(games))
	for i, game := range games {
		gamesResp[i] = *game.ToResp()
	}

	return ctx.JSON(http.StatusOK, map[string]interface{}{
		"games":    gamesResp,
		"nextPage": nextPage,
	})
}

// UpdateGame godoc
// @Summary      Update a game
// @Description  Updates game settings. Only the game owner can update.
// @Tags         Games
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        id   path string                  true "Game UUID"
// @Param        body body models.UpdateGameParams  true "Fields to update"
// @Success      200 {object} models.GameResp
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError "caller is not the game owner"
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{id} [put]
func UpdateGame(ctx echo.Context) error {
	id := ctx.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Game ID is required")
	}

	req := new(models.UpdateGameParams)
	if err := ctx.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	user := ctx.Get("user").(*models.User)
	game, err := models.UpdateGame(id, *req, *user)
	if err != nil {
		if errors.Is(err, models.ErrNotGameOwner) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error updating game: "+err.Error())
	}

	return ctx.JSON(http.StatusOK, game.ToResp())
}

// DeleteGame godoc
// @Summary      Delete a game
// @Description  Deletes a game. Only the game owner can delete.
// @Tags         Games
// @Produce      json
// @Security     BearerAuth
// @Param        id path string true "Game UUID"
// @Success      200 {object} map[string]string "message"
// @Failure      400 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError "caller is not the game owner"
// @Failure      500 {object} echo.HTTPError
// @Router       /game/{id} [delete]
func DeleteGame(ctx echo.Context) error {
	id := ctx.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Game ID is required")
	}

	user := ctx.Get("user").(*models.User)
	err := models.DeleteGame(id, *user)
	if err != nil {
		if errors.Is(err, models.ErrNotGameOwner) {
			return echo.NewHTTPError(http.StatusForbidden, err.Error())
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "Error deleting game: "+err.Error())
	}

	return ctx.JSON(http.StatusOK, echo.Map{"message": "Game deleted successfully"})
}

// func CreateGameSnapshot(ctx echo.Context) error {
// 	id := ctx.Param("id")
// 	if id == "" {
// 		return echo.NewHTTPError(http.StatusBadRequest, "Game ID is required")
// 	}

// 	game, err := models.GetGame(id)
// 	if err != nil {
// 		return echo.NewHTTPError(http.StatusInternalServerError, "Error getting game: "+err.Error())
// 	}

// 	snapshot, err := server.S.Machines.CreateSnapshot(ctx, game)

// 	return ctx.JSON(http.StatusOK, game.ToResp())
// }
