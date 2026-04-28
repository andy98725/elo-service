package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrLobbyNotFound = errors.New("lobby not found")
	ErrLobbyFull     = errors.New("lobby is full")
)

// addLobbyPlayerWithCapScript atomically: checks the current player count,
// rejects with 0 if it's at or above the cap, otherwise adds the player and
// sets their TTL key. Replaces a check-then-add sequence that allowed two
// concurrent joiners to both pass the cap check and exceed MaxPlayers.
//
// KEYS[1] = lobby_players_<lobbyID> (hash)
// KEYS[2] = lobby_player_ttl_<lobbyID>_<playerID> (string with expire)
// ARGV[1] = max_players, ARGV[2] = playerID, ARGV[3] = displayName,
// ARGV[4] = ttl in seconds.
var addLobbyPlayerWithCapScript = redis.NewScript(`
local count = redis.call('HLEN', KEYS[1])
local max = tonumber(ARGV[1])
if count >= max then
  return 0
end
redis.call('HSET', KEYS[1], ARGV[2], ARGV[3])
redis.call('SET', KEYS[2], '1', 'EX', ARGV[4])
return 1
`)

type LobbyRecord struct {
	ID           string    `json:"id"`
	GameID       string    `json:"game_id"`
	HostID       string    `json:"host_id"`
	HostName     string    `json:"host_name"`
	Tags         []string  `json:"tags"`
	Metadata     string    `json:"metadata"`
	MaxPlayers   int       `json:"max_players"`
	CreatedAt    time.Time `json:"created_at"`
	PasswordHash string    `json:"-"`
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
		"password_hash", lobby.PasswordHash,
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
		ID:           fields["id"],
		GameID:       fields["game_id"],
		HostID:       fields["host_id"],
		HostName:     fields["host_name"],
		Tags:         tags,
		Metadata:     fields["metadata"],
		MaxPlayers:   maxPlayers,
		CreatedAt:    createdAt,
		PasswordHash: fields["password_hash"],
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

// AddLobbyPlayer adds a player unconditionally, used by the host on lobby
// creation (no capacity gate needed — they're player 1).
func (r *Redis) AddLobbyPlayer(ctx context.Context, lobbyID, playerID, name string, ttl time.Duration) error {
	pipe := r.Client.Pipeline()
	pipe.HSet(ctx, lobbyPlayersKey(lobbyID), playerID, name)
	pipe.Set(ctx, lobbyPlayerTTLKey(lobbyID, playerID), "1", ttl)
	_, err := pipe.Exec(ctx)
	return err
}

// AddLobbyPlayerWithCap atomically rejects the join if the lobby is at or
// over maxPlayers; otherwise it adds the player and sets their TTL key.
// Returns ErrLobbyFull when the cap is reached. Uses a Lua script to close
// the TOCTOU window between count check and HSET.
func (r *Redis) AddLobbyPlayerWithCap(ctx context.Context, lobbyID, playerID, name string, maxPlayers int, ttl time.Duration) error {
	keys := []string{lobbyPlayersKey(lobbyID), lobbyPlayerTTLKey(lobbyID, playerID)}
	args := []interface{}{maxPlayers, playerID, name, int64(ttl.Seconds())}
	res, err := addLobbyPlayerWithCapScript.Run(ctx, r.Client, keys, args...).Int64()
	if err != nil {
		return err
	}
	if res == 0 {
		return ErrLobbyFull
	}
	return nil
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
	sub := r.Client.Subscribe(ctx, lobbyEventChannel(lobbyID))
	r.waitForSubscribeAck(ctx, lobbyEventChannel(lobbyID), "lobby_events", lobbyID)
	return sub
}

func (r *Redis) PublishLobbyKick(ctx context.Context, lobbyID, playerID, reason string) error {
	return r.Client.Publish(ctx, lobbyKickChannel(lobbyID, playerID), reason).Err()
}

func (r *Redis) WatchLobbyKick(ctx context.Context, lobbyID, playerID string) *redis.PubSub {
	sub := r.Client.Subscribe(ctx, lobbyKickChannel(lobbyID, playerID))
	r.waitForSubscribeAck(ctx, lobbyKickChannel(lobbyID, playerID), "lobby_kick", lobbyID+"_"+playerID)
	return sub
}

// waitForSubscribeAck blocks (with a short bounded timeout) until Redis
// has confirmed the SUBSCRIBE — i.e. until publishes to this subscription
// will actually be observed via .Channel(). Without this, Subscribe is
// effectively async: the call returns immediately, and a publish that
// races the SUBSCRIBE landing at the server is silently dropped.
//
// We use PubSubNumSub rather than sub.Receive because the latter reads a
// single frame from the connection — that races the .Channel() goroutine
// which is also trying to consume frames. NumSub asks the server "how
// many subscribers does this channel have right now," which is a
// different call entirely and doesn't perturb the connection.
//
// On timeout (or any other error) we log and return: the caller still
// gets a *PubSub, just with the same flaky behavior as before. The bound
// is short because we're racing the caller's first publish; if Redis
// hasn't acked in 2 seconds something is wrong.
func (r *Redis) waitForSubscribeAck(ctx context.Context, channel, kind, id string) {
	waitCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		counts, err := r.Client.PubSubNumSub(waitCtx, channel).Result()
		if err == nil && counts[channel] >= 1 {
			return
		}
		select {
		case <-waitCtx.Done():
			slog.Warn("subscribe ack timed out", "kind", kind, "id", id, "error", waitCtx.Err())
			return
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (r *Redis) AllLobbyIndexKeys(ctx context.Context) ([]string, error) {
	return r.Client.Keys(ctx, "lobby_index_*").Result()
}
