package match

import (
	"github.com/andy98725/elo-service/src/api/auth"
	"github.com/labstack/echo"
)

func InitRoutes(e *echo.Echo) error {
	// Matchmaking
	e.GET("/match/join", JoinQueueWebsocket, auth.RequireUserOrGuestAuth)
	e.GET("/match/size", QueueSize, auth.RequireUserOrGuestAuth)

	// CRUD
	e.GET("/match/:matchID", GetMatch, auth.RequireUserAuth)
	e.GET("/match/game/:gameID", GetMatchesOfGame, auth.RequireUserAuth)
	e.GET("/matches", GetMatches, auth.RequireAdmin)

	// Reconnect: rediscover active matches in a game after a page reload.
	e.GET("/games/:gameID/match/me", GetMyActiveMatches, auth.RequireUserOrGuestAuth)

	// Spectator discovery: list live matches in a spectate-enabled game.
	e.GET("/games/:gameID/matches/live", GetLiveMatchesInGame, auth.RequireUserOrGuestAuth)

	// Spectator stream proxy: long-poll over the S3-backed chunks for one match.
	e.GET("/matches/:matchID/stream", GetMatchStream, auth.RequireUserOrGuestAuth)

	// Game-server artifact upload: bytes auth'd by the per-match auth
	// code in Authorization: Bearer; no JWT middleware needed.
	e.POST("/match/artifact", UploadMatchArtifact)

	// Per-match artifact retrieval. Auth gated like /results/<id> —
	// participant/owner/admin always; PublicResults=true unlocks any auth.
	e.GET("/matches/:matchID/artifacts", ListMatchArtifacts, auth.RequireUserOrGuestAuth)
	e.GET("/matches/:matchID/artifacts/:name", DownloadMatchArtifact, auth.RequireUserOrGuestAuth)

	// Per-user artifact listing across games. Optional game_id + name= filters.
	e.GET("/user/artifacts", ListUserArtifacts, auth.RequireUserOrGuestAuth)

	return nil
}
