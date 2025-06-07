package game

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
)

type CreateGameRequest struct {
	Name                string `json:"name"`
	Description         string `json:"description"`
	GuestsAllowed       bool   `json:"guests_allowed"`
	MatchmakingStrategy string `json:"matchmaking_strategy"`
	ELOStrategy         string `json:"elo_strategy"`
}

func CreateGame(ctx echo.Context) error {
	req := new(CreateGameRequest)
	if err := ctx.Bind(req); err != nil {
		server.S.Logger.Warn("Error binding request", "error", err)
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}
	server.S.Logger.Info("Creating game", "game", req)

	user := ctx.Get("user").(*models.User)
	game, err := models.CreateGame(models.CreateGameParams{
		Name:                req.Name,
		Description:         req.Description,
		GuestsAllowed:       req.GuestsAllowed,
		MatchmakingStrategy: req.MatchmakingStrategy,
		ELOStrategy:         req.ELOStrategy,
	}, *user)
	if err != nil {
		errMsg := err.Error()
		if strings.Contains(errMsg, "duplicate key value violates unique constraint") {
			if strings.Contains(errMsg, "name") {
				return echo.NewHTTPError(http.StatusBadRequest, "game name already taken")
			}
			return echo.NewHTTPError(http.StatusBadRequest, "game already exists")
		}

		server.S.Logger.Error("Error creating game", "error", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "error creating game")
	}

	return ctx.JSON(http.StatusOK, game)
}

func GetGames(ctx echo.Context) error {
	page := ctx.QueryParam("page")
	pageSize := ctx.QueryParam("pageSize")

	pageInt, err := strconv.Atoi(page)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid page param")
	}
	pageSizeInt, err := strconv.Atoi(pageSize)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid pageSize param")
	}

	games, nextPage, err := models.GetGames(pageInt, pageSizeInt)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Error getting games: "+err.Error())
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
