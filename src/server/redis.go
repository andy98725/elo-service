package server

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrPlayerAlreadyInQueue = errors.New("player already in queue")
var ErrPlayerNotInQueue = errors.New("player not in queue")

type Redis struct {
	Client *redis.Client
}

func NewRedis(opt *redis.Options) *Redis {
	return &Redis{Client: redis.NewClient(opt)}
}

func (r *Redis) AddPlayerToQueue(ctx context.Context, gameID string, playerID string) error {
	_, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err == redis.Nil {
		// Player not found in queue, safe to add
		return r.Client.RPush(ctx, "queue_"+gameID, playerID).Err()
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
		_, err := pipe.Exec(ctx)
		return err
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
	return r.Client.LPopCount(ctx, "queue_"+gameID, count).Result()
}
func (r *Redis) PushPlayersToQueue(ctx context.Context, gameID string, playerIDs []string) error {
	interfacePlayers := make([]interface{}, len(playerIDs))
	for i, p := range playerIDs {
		interfacePlayers[i] = p
	}
	return r.Client.RPush(ctx, "queue_"+gameID, interfacePlayers...).Err()
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

func (r *Redis) AddMatchUnderway(ctx context.Context, machineName string) error {
	return r.Client.Set(ctx, "machine_"+machineName, time.Now().Unix(), 0).Err()
}

func (r *Redis) RemoveMatchUnderway(ctx context.Context, machineName string) (bool, error) {
	deleted, err := r.Client.Del(ctx, "machine_"+machineName).Result()
	return deleted > 0, err
}

func (r *Redis) MatchesUnderway(ctx context.Context) ([]string, error) {
	return r.Client.Keys(ctx, "machine_*").Result()
}

func (r *Redis) MatchStartedAt(ctx context.Context, machineName string) (time.Time, error) {
	return r.Client.Get(ctx, "machine_"+machineName).Time()
}
