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
	exists, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err != redis.Nil && err != nil {
		return err
	}
	if exists >= 0 {
		return ErrPlayerAlreadyInQueue
	}
	return r.Client.RPush(ctx, "queue_"+gameID, playerID).Err()
}

func (r *Redis) RemovePlayerFromQueue(ctx context.Context, gameID string, playerID string) error {
	exists, err := r.Client.LPos(ctx, "queue_"+gameID, playerID, redis.LPosArgs{}).Result()
	if err != redis.Nil && err != nil {
		return err
	}
	if exists < 0 {
		return ErrPlayerNotInQueue
	}
	return r.Client.LRem(ctx, "queue_"+gameID, 1, playerID).Err()
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

func (r *Redis) AddMatchUnderway(ctx context.Context, matchID string) error {
	return r.Client.Set(ctx, "match_started_"+matchID, time.Now().Unix(), 0).Err()
}

func (r *Redis) RemoveMatchUnderway(ctx context.Context, matchID string) error {
	return r.Client.Del(ctx, "match_started_"+matchID).Err()
}

func (r *Redis) MatchesUnderway(ctx context.Context) ([]string, error) {
	return r.Client.Keys(ctx, "match_started_*").Result()
}

func (r *Redis) MatchStartedAt(ctx context.Context, matchID string) (time.Time, error) {
	return r.Client.Get(ctx, "match_started_"+matchID).Time()
}
