package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrPlayerAlreadyInQueue = errors.New("player already in queue")
var ErrPlayerNotInQueue = errors.New("player not in queue")

// MetadataSeparator joins the gameID and the metadata fingerprint in a queue key.
// "::" never appears in a UUID gameID or in a hex-encoded hash, so the gameID is
// recoverable from the composite key by splitting on the first occurrence.
const MetadataSeparator = "::"

// QueueKey builds the queue identifier for a (gameID, metadata) pair. The
// metadata is treated as an opaque byte string and hashed, so arbitrary
// client-defined values (including JSON) produce a fixed-size, charset-safe
// suffix. Two clients are placed in the same queue iff their metadata bytes
// are identical -- callers that want logical equality (e.g. JSON with
// reordered keys) must canonicalize before passing it in.
func QueueKey(gameID string, metadata string) string {
	if metadata == "" {
		return gameID
	}
	sum := sha256.Sum256([]byte(metadata))
	return gameID + MetadataSeparator + hex.EncodeToString(sum[:])
}

// ParseQueueKey extracts the gameID from a composite queue key produced by
// QueueKey. The metadata fingerprint is intentionally not returned: the
// matchmaking worker only needs the gameID for game-config lookups, and the
// original metadata cannot be recovered from its hash.
func ParseQueueKey(queueKey string) (gameID string) {
	if idx := strings.Index(queueKey, MetadataSeparator); idx != -1 {
		return queueKey[:idx]
	}
	return queueKey
}

type Redis struct {
	Client *redis.Client
}

func NewRedis(redisURL string) (*Redis, error) {
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Redis URL: %v", err)
	}

	client := &Redis{Client: redis.NewClient(opt)}
	if err := client.Client.Ping(context.Background()).Err(); err != nil {
		return nil, fmt.Errorf("Redis ping failed (is Redis running and REDIS_URL correct?): %v", err)
	}

	return client, nil
}

func (r *Redis) AddPlayerToQueue(ctx context.Context, gameID string, playerID string) error {
	_, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err == redis.Nil {
		// Player not found in queue, safe to add
		if err := r.Client.RPush(ctx, "queue_"+gameID, playerID).Err(); err != nil {
			return err
		}
		_ = r.PublishMatchmakingTrigger(ctx) // best-effort wake worker
		return nil
	}
	if err != nil {
		return err
	}
	// Player found in queue
	return ErrPlayerAlreadyInQueue
}

// AddPlayerToQueueWithTTL adds a player to the queue with individual TTL
// Uses separate keys for each player to allow per-player TTL management
func (r *Redis) AddPlayerToQueueWithTTL(ctx context.Context, gameID string, playerID string, ttl time.Duration) error {
	// Check if player is already in queue
	_, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err == redis.Nil {
		// Player not found in queue, safe to add
		pipe := r.Client.Pipeline()
		pipe.RPush(ctx, "queue_"+gameID, playerID)
		// Set individual TTL for this player
		pipe.Set(ctx, "player_queue_"+gameID+"_"+playerID, "1", ttl)
		// Record join timestamp for rating-based matchmaking's
		// wait-window expansion. Stored on every queue (not just rating
		// games) so that toggling MatchmakingStrategy on a game doesn't
		// leave stale entries with no timestamp.
		pipe.HSet(ctx, "qjoined_"+gameID, playerID, time.Now().Unix())
		if _, err := pipe.Exec(ctx); err != nil {
			return err
		}
		_ = r.PublishMatchmakingTrigger(ctx) // best-effort wake worker
		return nil
	}
	if err != nil {
		return err
	}
	// Player found in queue
	return ErrPlayerAlreadyInQueue
}

func (r *Redis) RemovePlayerFromQueue(ctx context.Context, gameID string, playerID string) error {
	_, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err == redis.Nil {
		// Player not found in queue
		return ErrPlayerNotInQueue
	}
	if err != nil {
		return err
	}
	// Player found in queue, safe to remove
	pipe := r.Client.Pipeline()
	pipe.LRem(ctx, "queue_"+gameID, 1, playerID)
	// Also remove the individual player TTL key
	pipe.Del(ctx, "player_queue_"+gameID+"_"+playerID)
	pipe.HDel(ctx, "qjoined_"+gameID, playerID)
	_, err = pipe.Exec(ctx)
	return err
}

// RefreshPlayerQueueTTL extends the TTL for a specific player in the queue
func (r *Redis) RefreshPlayerQueueTTL(ctx context.Context, gameID string, playerID string, ttl time.Duration) error {
	return r.Client.Expire(ctx, "player_queue_"+gameID+"_"+playerID, ttl).Err()
}

// IsPlayerConnectionAlive checks if a player is still in queue by checking their TTL key
func (r *Redis) IsPlayerConnectionAlive(ctx context.Context, gameID string, playerID string) (bool, error) {
	exists, err := r.Client.Exists(ctx, "player_queue_"+gameID+"_"+playerID).Result()
	if err != nil {
		return false, err
	}
	return exists > 0, nil
}

func (r *Redis) AllPlayersInQueue(ctx context.Context, gameID string) ([]string, error) {
	return r.Client.LRange(ctx, "queue_"+gameID, 0, -1).Result()
}

func (r *Redis) PopPlayersFromQueue(ctx context.Context, gameID string, count int) ([]string, error) {
	players, err := r.Client.LPopCount(ctx, "queue_"+gameID, count).Result()
	if err != nil {
		return nil, err
	}
	if len(players) > 0 {
		fields := make([]string, len(players))
		copy(fields, players)
		_ = r.Client.HDel(ctx, "qjoined_"+gameID, fields...).Err()
	}
	return players, nil
}

// RemovePlayersFromQueue removes a specific set of players from a queue and
// drops their TTL + join-time records. Used by rating-based matchmaking,
// which selects pairs by rating window rather than popping the front of
// the list.
func (r *Redis) RemovePlayersFromQueue(ctx context.Context, gameID string, playerIDs []string) error {
	if len(playerIDs) == 0 {
		return nil
	}
	pipe := r.Client.Pipeline()
	for _, p := range playerIDs {
		pipe.LRem(ctx, "queue_"+gameID, 1, p)
		pipe.Del(ctx, "player_queue_"+gameID+"_"+p)
	}
	fields := make([]string, len(playerIDs))
	copy(fields, playerIDs)
	pipe.HDel(ctx, "qjoined_"+gameID, fields...)
	_, err := pipe.Exec(ctx)
	return err
}

// QueueJoinTimes returns the unix-second join timestamps for every player
// currently tracked in the queue's qjoined hash. Players missing from the
// hash (e.g. left over from a queue that pre-dates the hash) are simply
// absent — the caller should treat that as "joined long enough ago that
// the window is fully expanded."
func (r *Redis) QueueJoinTimes(ctx context.Context, gameID string) (map[string]int64, error) {
	raw, err := r.Client.HGetAll(ctx, "qjoined_"+gameID).Result()
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(raw))
	for k, v := range raw {
		var ts int64
		fmt.Sscanf(v, "%d", &ts)
		out[k] = ts
	}
	return out, nil
}

func (r *Redis) PushPlayersToQueue(ctx context.Context, gameID string, playerIDs []string) error {
	interfacePlayers := make([]interface{}, len(playerIDs))
	for i, p := range playerIDs {
		interfacePlayers[i] = p
	}
	pipe := r.Client.Pipeline()
	pipe.RPush(ctx, "queue_"+gameID, interfacePlayers...)
	now := time.Now().Unix()
	for _, p := range playerIDs {
		pipe.HSetNX(ctx, "qjoined_"+gameID, p, now)
	}
	_, err := pipe.Exec(ctx)
	return err
}

func (r *Redis) GameQueueSize(ctx context.Context, gameID string) (int64, error) {
	return r.Client.LLen(ctx, "queue_"+gameID).Result()
}

func (r *Redis) AllQueues(ctx context.Context) ([]string, error) {
	return r.Client.Keys(ctx, "queue_*").Result()
}

func (r *Redis) WatchMatchReady(ctx context.Context, gameID string, playerID string) *redis.PubSub {
	return r.Client.Subscribe(ctx, "match_ready_"+gameID+"__"+playerID)
}

func (r *Redis) PublishMatchReady(ctx context.Context, gameID string, playerID string, message string) error {
	return r.Client.Publish(ctx, "match_ready_"+gameID+"__"+playerID, message).Err()
}

const MatchmakingTriggerChannel = "trigger_matchmaking"

func (r *Redis) SubscribeMatchmakingTrigger(ctx context.Context) *redis.PubSub {
	return r.Client.Subscribe(ctx, MatchmakingTriggerChannel)
}

func (r *Redis) PublishMatchmakingTrigger(ctx context.Context) error {
	return r.Client.Publish(ctx, MatchmakingTriggerChannel, "1").Err()
}

const GarbageCollectionTriggerChannel = "trigger_garbage_collection"

func (r *Redis) SubscribeGarbageCollectionTrigger(ctx context.Context) *redis.PubSub {
	return r.Client.Subscribe(ctx, GarbageCollectionTriggerChannel)
}

func (r *Redis) PublishGarbageCollectionTrigger(ctx context.Context) error {
	return r.Client.Publish(ctx, GarbageCollectionTriggerChannel, "1").Err()
}
