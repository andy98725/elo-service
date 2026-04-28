package playerData

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// entriesResponse is the shape returned by both list endpoints. Keyed
// object (not array) because clients almost always want lookup by key.
type entriesResponse struct {
	Entries map[string]json.RawMessage `json:"entries"`
}

func entriesToResponse(entries []models.PlayerGameEntry) entriesResponse {
	out := make(map[string]json.RawMessage, len(entries))
	for _, e := range entries {
		out[e.Key] = e.Value
	}
	return entriesResponse{Entries: out}
}

func playerIDFromContext(c echo.Context) (string, bool) {
	id, ok := c.Get("id").(string)
	return id, ok && id != ""
}

// ListMyPlayerEntries godoc
// @Summary      List own player-authored entries
// @Description  Returns every player-authored entry the caller has written for the given game. See server endpoint to read entries written by the game server.
// @Tags         PlayerData
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "entries"
// @Failure      401 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/me/player [get]
func ListMyPlayerEntries(c echo.Context) error {
	return listMine(c, false)
}

// ListMyServerEntries godoc
// @Summary      List own server-authored entries
// @Description  Returns every server-authored entry the game server has written for the calling player in the given game. Read-only from the player side.
// @Tags         PlayerData
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Success      200 {object} map[string]interface{} "entries"
// @Failure      401 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/me/server [get]
func ListMyServerEntries(c echo.Context) error {
	return listMine(c, true)
}

func listMine(c echo.Context, serverAuthored bool) error {
	gameID := c.Param("gameID")
	playerID, ok := playerIDFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	entries, err := models.ListPlayerGameEntries(gameID, playerID, serverAuthored)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error listing entries: "+err.Error())
	}
	return c.JSON(http.StatusOK, entriesToResponse(entries))
}

// UpsertMyEntry godoc
// @Summary      Upsert a player-authored entry
// @Description  Writes a player-authored JSON value for the calling player at the given key. Replaces any existing player-authored entry at that key. The same key may also exist as a server-authored entry — the two are independent.
// @Tags         PlayerData
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Param        key    path string true "Entry key (a-zA-Z0-9._-, max 128 chars)"
// @Param        body   body object true "Arbitrary JSON value (max 64KB)"
// @Success      200 {object} map[string]string "status"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      413 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/me/{key} [put]
func UpsertMyEntry(c echo.Context) error {
	playerID, ok := playerIDFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	return upsertEntry(c, c.Param("gameID"), playerID, c.Param("key"), false)
}

// DeleteMyEntry godoc
// @Summary      Delete a player-authored entry
// @Description  Removes the calling player's player-authored entry at the given key. Does not affect any server-authored entry at the same key.
// @Tags         PlayerData
// @Produce      json
// @Security     BearerAuth
// @Param        gameID path string true "Game UUID"
// @Param        key    path string true "Entry key"
// @Success      200 {object} map[string]string "status"
// @Failure      400 {object} echo.HTTPError
// @Failure      401 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      500 {object} echo.HTTPError
// @Router       /games/{gameID}/data/me/{key} [delete]
func DeleteMyEntry(c echo.Context) error {
	playerID, ok := playerIDFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
	return deleteEntry(c, c.Param("gameID"), playerID, c.Param("key"), false)
}

// upsertEntry is shared by player- and server-side write handlers. It
// enforces key + value validation and translates model errors into HTTP
// status codes.
func upsertEntry(c echo.Context, gameID, playerID, key string, serverAuthored bool) error {
	if err := models.ValidatePlayerGameEntryKey(key); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid key: must match [a-zA-Z0-9._-]{1,128}")
	}

	body, err := readBoundedBody(c, models.PlayerGameEntryMaxValueBytes)
	if err != nil {
		return err
	}

	if err := models.ValidatePlayerGameEntryValue(body); err != nil {
		switch {
		case errors.Is(err, models.ErrPlayerGameEntryValueTooLarge):
			return echo.NewHTTPError(http.StatusRequestEntityTooLarge, "value exceeds 64KB")
		case errors.Is(err, models.ErrPlayerGameEntryValueInvalid):
			return echo.NewHTTPError(http.StatusBadRequest, "value is not valid JSON")
		default:
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}

	if err := models.UpsertPlayerGameEntry(gameID, playerID, key, serverAuthored, json.RawMessage(body)); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error writing entry: "+err.Error())
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}

func deleteEntry(c echo.Context, gameID, playerID, key string, serverAuthored bool) error {
	if err := models.ValidatePlayerGameEntryKey(key); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid key")
	}
	if err := models.DeletePlayerGameEntry(gameID, playerID, key, serverAuthored); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "entry not found")
		}
		return echo.NewHTTPError(http.StatusInternalServerError, "error deleting entry: "+err.Error())
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}
