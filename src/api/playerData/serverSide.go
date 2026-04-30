package playerData

import (
	"net/http"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/util"
	"github.com/labstack/echo"
)

// matchContextKey is the echo.Context key under which requireMatchAuth
// stashes the resolved match so handlers don't re-query.
const matchContextKey = "playerData.match"

// requireMatchAuth resolves and validates the match auth code carried in
// the Authorization header, then enforces:
//   - the match is still underway OR within its post-result cooldown
//     window (not torn down)
//   - the URL :gameID matches the match's game
//   - the URL :playerID is in the match (and is not a guest)
//
// On success the handler can pull the match from the context with
// c.Get(matchContextKey).(*models.Match).
//
// The cooldown allowance lets game servers finish post-match
// player_data writes after /result/report without racing the worker
// teardown. Once the cooldown sweep deletes the Match row, the auth
// code stops resolving and callers get 401.
func requireMatchAuth(handler echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		token := c.Request().Header.Get("Authorization")
		if strings.HasPrefix(token, "Bearer ") {
			token = strings.TrimPrefix(token, "Bearer ")
		}
		if token == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "missing match auth token")
		}

		match, err := models.GetMatchByTokenID(token)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "invalid match auth token")
		}
		if !models.IsMatchActiveOrCooling(match) {
			return echo.NewHTTPError(http.StatusForbidden, "match is not underway")
		}

		gameID := c.Param("gameID")
		if gameID == "" || gameID != match.GameID {
			return echo.NewHTTPError(http.StatusForbidden, "gameID does not match this match's game")
		}

		playerID := c.Param("playerID")
		if playerID == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "playerID is required")
		}
		if util.IsGuestID(playerID) {
			return echo.NewHTTPError(http.StatusBadRequest, "guests do not have player data")
		}
		if !matchIncludesPlayer(match, playerID) {
			return echo.NewHTTPError(http.StatusForbidden, "player is not in this match")
		}

		c.Set(matchContextKey, match)
		return handler(c)
	}
}

func matchIncludesPlayer(match *models.Match, playerID string) bool {
	for _, p := range match.Players {
		if p.ID == playerID {
			return true
		}
	}
	for _, g := range match.GuestIDs {
		if g == playerID {
			return true
		}
	}
	return false
}

// ListPlayerEntriesAsServer godoc
// @Summary      List a player's player-authored entries (game server)
// @Description  Read the player-authored entries for one of the players in the active match. Auth is the match auth code, passed as Authorization: Bearer <code>.
// @Tags         PlayerData
// @Produce      json
// @Param        gameID   path string true "Game UUID"
// @Param        playerID path string true "Player UUID (registered users only — guests rejected with 400)"
// @Success      200 {object} map[string]interface{} "entries"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/{playerID}/player [get]
func ListPlayerEntriesAsServer(c echo.Context) error {
	return requireMatchAuth(func(c echo.Context) error {
		return listAsServer(c, false)
	})(c)
}

// ListServerEntriesAsServer godoc
// @Summary      List a player's server-authored entries (game server)
// @Description  Read the server-authored entries for one of the players in the active match. Auth is the match auth code, passed as Authorization: Bearer <code>.
// @Tags         PlayerData
// @Produce      json
// @Param        gameID   path string true "Game UUID"
// @Param        playerID path string true "Player UUID (registered users only — guests rejected with 400)"
// @Success      200 {object} map[string]interface{} "entries"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/{playerID}/server [get]
func ListServerEntriesAsServer(c echo.Context) error {
	return requireMatchAuth(func(c echo.Context) error {
		return listAsServer(c, true)
	})(c)
}

func listAsServer(c echo.Context, serverAuthored bool) error {
	match := c.Get(matchContextKey).(*models.Match)
	playerID := c.Param("playerID")
	entries, err := models.ListPlayerGameEntries(match.GameID, playerID, serverAuthored)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error listing entries: "+err.Error())
	}
	return c.JSON(http.StatusOK, entriesToResponse(entries))
}

// UpsertEntryAsServer godoc
// @Summary      Upsert a server-authored entry (game server)
// @Description  Writes a server-authored JSON value for a player in the active match. Replaces any existing server-authored entry at that key. The same key may also exist as a player-authored entry — the two are independent.
// @Tags         PlayerData
// @Accept       json
// @Produce      json
// @Param        gameID   path string true "Game UUID"
// @Param        playerID path string true "Player UUID (registered users only)"
// @Param        key      path string true "Entry key (a-zA-Z0-9._-, max 128 chars)"
// @Param        body     body object true "Arbitrary JSON value (max 64KB)"
// @Success      200 {object} map[string]string "status"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      413 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/{playerID}/{key} [put]
func UpsertEntryAsServer(c echo.Context) error {
	return requireMatchAuth(func(c echo.Context) error {
		match := c.Get(matchContextKey).(*models.Match)
		return upsertEntry(c, match.GameID, c.Param("playerID"), c.Param("key"), true)
	})(c)
}

// DeleteEntryAsServer godoc
// @Summary      Delete a server-authored entry (game server)
// @Description  Removes a server-authored entry for a player in the active match. Does not affect any player-authored entry at the same key.
// @Tags         PlayerData
// @Produce      json
// @Param        gameID   path string true "Game UUID"
// @Param        playerID path string true "Player UUID (registered users only)"
// @Param        key      path string true "Entry key"
// @Success      200 {object} map[string]string "status"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      403 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/{playerID}/{key} [delete]
func DeleteEntryAsServer(c echo.Context) error {
	return requireMatchAuth(func(c echo.Context) error {
		match := c.Get(matchContextKey).(*models.Match)
		return deleteEntry(c, match.GameID, c.Param("playerID"), c.Param("key"), true)
	})(c)
}

