package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrLobbyNotFound = errors.New("lobby not found")

type LobbyRecord struct {
	ID         string    `json:"id"`
	GameID     string    `json:"game_id"`
	HostID     string    `json:"host_id"`
	HostName   string    `json:"host_name"`
	Tags       []string  `json:"tags"`
	Metadata   string    `json:"metadata"`
	MaxPlayers int       `json:"max_players"`
	CreatedAt  time.Time `json:"created_at"`
}

func lobbyKey(lobbyID string) string         { return "lobby_" + lobbyID }
func lobbyPlayersKey(lobbyID string) string  { return "lobby_players_" + lobbyID }
func lobbyIndexKey(gameID string) string     { return "lobby_index_" + gameID }
func lobbyEventChannel(lobbyID string) string {
	return "lobby_events_" + lobbyID
}
func lobbyKickChannel(lobbyID, playerID string) string {
	return "lobby_kick_" + lobbyID + "_" + playerID
}
func lobbyPlayerTTLKey(lobbyID, playerID string) string {
	return "lobby_player_ttl_" + lobbyID + "_" + playerID
}

func (r *Redis) CreateLobby(ctx context.Context, lobby *LobbyRecord) error {
	tagsJSON, err := json.Marshal(lobby.Tags)
	if err != nil {
		return err
	}
	pipe := r.Client.Pipeline()
	pipe.HSet(ctx, lobbyKey(lobby.ID),
		"id", lobby.ID,
		"game_id", lobby.GameID,
		"host_id", lobby.HostID,
		"host_name", lobby.HostName,
		"tags", string(tagsJSON),
		"metadata", lobby.Metadata,
		"max_players", strconv.Itoa(lobby.MaxPlayers),
		"created_at", lobby.CreatedAt.Format(time.RFC3339Nano),
	)
	pipe.SAdd(ctx, lobbyIndexKey(lobby.GameID), lobby.ID)
	_, err = pipe.Exec(ctx)
	return err
}

func (r *Redis) GetLobby(ctx context.Context, lobbyID string) (*LobbyRecord, error) {
	fields, err := r.Client.HGetAll(ctx, lobbyKey(lobbyID)).Result()
	if err != nil {
		return nil, err
	}
	if len(fields) == 0 {
		return nil, ErrLobbyNotFound
	}
	maxPlayers, _ := strconv.Atoi(fields["max_players"])
	createdAt, _ := time.Parse(time.RFC3339Nano, fields["created_at"])
	var tags []string
	if raw := fields["tags"]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &tags)
	}
	return &LobbyRecord{
		ID:         fields["id"],
		GameID:     fields["game_id"],
		HostID:     fields["host_id"],
		HostName:   fields["host_name"],
		Tags:       tags,
		Metadata:   fields["metadata"],
		MaxPlayers: maxPlayers,
		CreatedAt:  createdAt,
	}, nil
}

func (r *Redis) DeleteLobby(ctx context.Context, lobbyID, gameID string) error {
	pipe := r.Client.Pipeline()
	pipe.Del(ctx, lobbyKey(lobbyID))
	pipe.Del(ctx, lobbyPlayersKey(lobbyID))
	pipe.SRem(ctx, lobbyIndexKey(gameID), lobbyID)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) AddLobbyPlayer(ctx context.Context, lobbyID, playerID, name string, ttl time.Duration) error {
	pipe := r.Client.Pipeline()
	pipe.HSet(ctx, lobbyPlayersKey(lobbyID), playerID, name)
	pipe.Set(ctx, lobbyPlayerTTLKey(lobbyID, playerID), "1", ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) RemoveLobbyPlayer(ctx context.Context, lobbyID, playerID string) error {
	pipe := r.Client.Pipeline()
	pipe.HDel(ctx, lobbyPlayersKey(lobbyID), playerID)
	pipe.Del(ctx, lobbyPlayerTTLKey(lobbyID, playerID))
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) RefreshLobbyPlayerTTL(ctx context.Context, lobbyID, playerID string, ttl time.Duration) error {
	return r.Client.Expire(ctx, lobbyPlayerTTLKey(lobbyID, playerID), ttl).Err()
}

func (r *Redis) IsLobbyPlayerAlive(ctx context.Context, lobbyID, playerID string) (bool, error) {
	exists, err := r.Client.Exists(ctx, lobbyPlayerTTLKey(lobbyID, playerID)).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *Redis) LobbyPlayers(ctx context.Context, lobbyID string) (map[string]string, error) {
	return r.Client.HGetAll(ctx, lobbyPlayersKey(lobbyID)).Result()
}

func (r *Redis) LobbyPlayerCount(ctx context.Context, lobbyID string) (int64, error) {
	return r.Client.HLen(ctx, lobbyPlayersKey(lobbyID)).Result()
}

func (r *Redis) FindLobbyPlayerByName(ctx context.Context, lobbyID, name string) (string, error) {
	players, err := r.LobbyPlayers(ctx, lobbyID)
	if err != nil {
		return "", err
	}
	for id, n := range players {
		if n == name {
			return id, nil
		}
	}
	return "", fmt.Errorf("player %q not found in lobby", name)
}

func (r *Redis) LobbiesForGame(ctx context.Context, gameID string) ([]*LobbyRecord, error) {
	ids, err := r.Client.SMembers(ctx, lobbyIndexKey(gameID)).Result()
	if err != nil {
		return nil, err
	}
	out := make([]*LobbyRecord, 0, len(ids))
	for _, id := range ids {
		lobby, err := r.GetLobby(ctx, id)
		if err == ErrLobbyNotFound {
			r.Client.SRem(ctx, lobbyIndexKey(gameID), id)
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, lobby)
	}
	return out, nil
}

func (r *Redis) PublishLobbyEvent(ctx context.Context, lobbyID, payload string) error {
	return r.Client.Publish(ctx, lobbyEventChannel(lobbyID), payload).Err()
}

func (r *Redis) WatchLobbyEvents(ctx context.Context, lobbyID string) *redis.PubSub {
	return r.Client.Subscribe(ctx, lobbyEventChannel(lobbyID))
}

func (r *Redis) PublishLobbyKick(ctx context.Context, lobbyID, playerID, reason string) error {
	return r.Client.Publish(ctx, lobbyKickChannel(lobbyID, playerID), reason).Err()
}

func (r *Redis) WatchLobbyKick(ctx context.Context, lobbyID, playerID string) *redis.PubSub {
	return r.Client.Subscribe(ctx, lobbyKickChannel(lobbyID, playerID))
}

func (r *Redis) AllLobbyIndexKeys(ctx context.Context) ([]string, error) {
	return r.Client.Keys(ctx, "lobby_index_*").Result()
}
