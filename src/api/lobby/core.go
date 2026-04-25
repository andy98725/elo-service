package lobby

import (
	"slices"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

const (
	LOBBY_PLAYER_TTL              = 2 * time.Minute
	LOBBY_PLAYER_REFRESH_INTERVAL = 30 * time.Second
)

type lobbyEvent struct {
	Event   string `json:"event"`
	ID      string `json:"id,omitempty"`
	Name    string `json:"name,omitempty"`
	Message string `json:"message,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type LobbyResp struct {
	ID         string    `json:"id"`
	GameID     string    `json:"game_id"`
	HostID     string    `json:"host_id"`
	HostName   string    `json:"host_name"`
	Tags       []string  `json:"tags"`
	Metadata   string    `json:"metadata"`
	Players    int       `json:"players"`
	MaxPlayers int       `json:"max_players"`
	CreatedAt  time.Time `json:"created_at"`
}

func toResp(rec *redis.LobbyRecord, players int) *LobbyResp {
	return &LobbyResp{
		ID:         rec.ID,
		GameID:     rec.GameID,
		HostID:     rec.HostID,
		HostName:   rec.HostName,
		Tags:       rec.Tags,
		Metadata:   rec.Metadata,
		Players:    players,
		MaxPlayers: rec.MaxPlayers,
		CreatedAt:  rec.CreatedAt,
	}
}

func displayName(c echo.Context) string {
	if u, ok := c.Get("user").(*models.User); ok && u != nil {
		return u.Username
	}
	if g, ok := c.Get("guest").(models.Guest); ok && g.DisplayName != "" {
		return g.DisplayName
	}
	if id, ok := c.Get("id").(string); ok {
		return id
	}
	return ""
}

func parseTags(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func tagsContainAll(have, want []string) bool {
	for _, t := range want {
		if !slices.Contains(have, t) {
			return false
		}
	}
	return true
}
