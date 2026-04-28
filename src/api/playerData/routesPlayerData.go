// Package playerData implements the per-(game, player) JSON KV backend.
//
// Two namespaces share a (game, player) row: player-authored entries are
// written by the owning player via JWT auth, server-authored entries are
// written by the active match's game server using the match auth code.
// Each side can read both halves but only write its own.
//
// Guests are intentionally unsupported — guest IDs are not persisted in
// the users table, so there's no FK target and no recovery path for the
// holder when their ephemeral token is lost.
package playerData

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Player-side: JWT auth, scoped to the caller's own player ID.
	e.GET("/games/:gameID/data/me/player", ListMyPlayerEntries, auth.RequireUserAuth)
	e.GET("/games/:gameID/data/me/server", ListMyServerEntries, auth.RequireUserAuth)
	e.PUT("/games/:gameID/data/me/:key", UpsertMyEntry, auth.RequireUserAuth)
	e.DELETE("/games/:gameID/data/me/:key", DeleteMyEntry, auth.RequireUserAuth)

	// Server-side: match-auth via Authorization header carrying the
	// match's auth code. Game server can only touch entries for players
	// in its current match.
	e.GET("/games/:gameID/data/:playerID/player", ListPlayerEntriesAsServer)
	e.GET("/games/:gameID/data/:playerID/server", ListServerEntriesAsServer)
	e.PUT("/games/:gameID/data/:playerID/:key", UpsertEntryAsServer)
	e.DELETE("/games/:gameID/data/:playerID/:key", DeleteEntryAsServer)

	return nil
}
