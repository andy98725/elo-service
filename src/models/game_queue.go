package models

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
)

// ErrLastQueue is returned when DeleteGameQueue is called on the only
// remaining queue for a game. A game must always have at least one queue;
// otherwise /match/join with no queueID has nothing to default to.
// Handlers should map this to HTTP 409.
var ErrLastQueue = errors.New("cannot delete the last remaining queue for a game")

// ErrNotQueueOwner is returned when a queue mutation comes from a user who
// does not own the parent game. Handlers should map this to HTTP 403.
var ErrNotQueueOwner = errors.New("not the owner of this game queue")

const DefaultQueueName = "primary"
const DefaultMatchmakingMachineName = "docker.io/andy98725/example-server:latest"

// GameQueue is a matchmaking pool within a Game. One Game can have many
// queues (e.g. "ranked-1v1", "casual-2v2", "stress-test-image"). Default
// queue = the oldest one (ORDER BY created_at, id), referenced when the
// caller hits /match/join with no queueID.
//
// Queues hold every per-pool knob: image, ports, lobby/elo settings,
// metadata segmentation. The parent Game keeps only identity, ownership,
// and game-wide policy (PublicResults, PublicMatchLogs, GuestsAllowed,
// SpectateEnabled).
type GameQueue struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID    string    `json:"game_id" gorm:"not null;uniqueIndex:idx_game_queue_name"`
	Game      Game      `json:"-" gorm:"foreignKey:GameID;constraint:OnDelete:CASCADE"`
	Name      string    `json:"name" gorm:"not null;uniqueIndex:idx_game_queue_name"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`

	// `default:true` tags are deliberately omitted from LobbyEnabled and
	// MetadataEnabled (mirroring the same trap that bit Game). Defaults
	// are enforced in CreateGameQueue instead so an explicit false from
	// the caller isn't silently flipped to true.
	LobbyEnabled            bool          `json:"lobby_enabled"`
	LobbySize               int           `json:"lobby_size" gorm:"default:2"`
	MatchmakingStrategy     string        `json:"matchmaking_strategy" gorm:"not null;default:'random'"`
	MatchmakingMachineName  string        `json:"matchmaking_machine_name" gorm:"not null"`
	MatchmakingMachinePorts pq.Int64Array `json:"matchmaking_machine_ports" gorm:"type:integer[];default:'{}'"`
	ELOStrategy             string        `json:"elo_strategy" gorm:"not null;default:'unranked'"`
	DefaultRating           int           `json:"default_rating" gorm:"default:1000"`
	KFactor                 int           `json:"k_factor" gorm:"default:32"`
	MetadataEnabled         bool          `json:"metadata_enabled"`
}

type GameQueueResp struct {
	ID                      string  `json:"id"`
	GameID                  string  `json:"game_id"`
	Name                    string  `json:"name"`
	LobbyEnabled            bool    `json:"lobby_enabled"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
	DefaultRating           int     `json:"default_rating"`
	KFactor                 int     `json:"k_factor"`
	MetadataEnabled         bool    `json:"metadata_enabled"`
}

func (q *GameQueue) ToResp() *GameQueueResp {
	return &GameQueueResp{
		ID:                      q.ID,
		GameID:                  q.GameID,
		Name:                    q.Name,
		LobbyEnabled:            q.LobbyEnabled,
		LobbySize:               q.LobbySize,
		MatchmakingStrategy:     q.MatchmakingStrategy,
		MatchmakingMachineName:  q.MatchmakingMachineName,
		MatchmakingMachinePorts: []int64(q.MatchmakingMachinePorts),
		ELOStrategy:             q.ELOStrategy,
		DefaultRating:           q.DefaultRating,
		KFactor:                 q.KFactor,
		MetadataEnabled:         q.MetadataEnabled,
	}
}

type CreateGameQueueParams struct {
	Name                    string
	LobbyEnabled            *bool
	LobbySize               int
	MatchmakingStrategy     string
	MatchmakingMachineName  string
	MatchmakingMachinePorts []int64
	ELOStrategy             string
	DefaultRating           int
	KFactor                 int
	MetadataEnabled         *bool
}

// applyQueueDefaults fills in defaults and validates strategy fields.
// Shared by CreateGameQueue and the in-tx default-queue creation path
// inside CreateGame.
func applyQueueDefaults(p *CreateGameQueueParams) error {
	if p.Name == "" {
		p.Name = DefaultQueueName
	}
	if p.MatchmakingMachineName == "" {
		p.MatchmakingMachineName = DefaultMatchmakingMachineName
	}
	if p.MatchmakingStrategy == "" {
		p.MatchmakingStrategy = MATCHMAKING_STRATEGY_RANDOM
	}
	if !slices.Contains(MATCHMAKING_STRATEGIES, p.MatchmakingStrategy) {
		return errors.New("invalid matchmaking strategy: " + p.MatchmakingStrategy + " must be one of " + strings.Join(MATCHMAKING_STRATEGIES, ", "))
	}
	if p.ELOStrategy == "" {
		p.ELOStrategy = ELO_STRATEGY_UNRANKED
	}
	if !slices.Contains(ELO_STRATEGIES, p.ELOStrategy) {
		return errors.New("invalid elo strategy: " + p.ELOStrategy + " must be one of " + strings.Join(ELO_STRATEGIES, ", "))
	}
	if p.LobbySize == 0 {
		p.LobbySize = 2
	}
	if p.KFactor == 0 {
		p.KFactor = 32
	}
	if p.DefaultRating == 0 {
		p.DefaultRating = 1000
	}
	return nil
}

// queueFromParams builds a GameQueue struct (not yet persisted) from
// validated params. Caller is responsible for setting GameID and Create()ing.
//
// CreatedAt is stamped with Go's nanosecond-resolution time rather than
// left to the SQL CURRENT_TIMESTAMP default. The default-queue lookup
// orders by (created_at ASC, id ASC), and the SQLite test harness stores
// CURRENT_TIMESTAMP at second resolution — two queues created in quick
// succession would tie on created_at, and the random-UUID tiebreaker
// would pick the queue order non-deterministically. Stamping in Go gives
// us strict ordering across calls.
func queueFromParams(p CreateGameQueueParams) *GameQueue {
	lobbyEnabled := true
	if p.LobbyEnabled != nil {
		lobbyEnabled = *p.LobbyEnabled
	}
	metadataEnabled := false
	if p.MetadataEnabled != nil {
		metadataEnabled = *p.MetadataEnabled
	}
	return &GameQueue{
		Name:                    p.Name,
		CreatedAt:               time.Now().UTC(),
		LobbyEnabled:            lobbyEnabled,
		LobbySize:               p.LobbySize,
		MatchmakingStrategy:     p.MatchmakingStrategy,
		MatchmakingMachineName:  p.MatchmakingMachineName,
		MatchmakingMachinePorts: pq.Int64Array(p.MatchmakingMachinePorts),
		ELOStrategy:             p.ELOStrategy,
		DefaultRating:           p.DefaultRating,
		KFactor:                 p.KFactor,
		MetadataEnabled:         metadataEnabled,
	}
}

// CreateGameQueue persists a new queue under an existing game. Caller
// must have already verified game ownership.
func CreateGameQueue(gameID string, params CreateGameQueueParams) (*GameQueue, error) {
	if err := applyQueueDefaults(&params); err != nil {
		return nil, err
	}
	q := queueFromParams(params)
	q.GameID = gameID
	if err := server.S.DB.Create(q).Error; err != nil {
		return nil, err
	}
	return q, nil
}

// GetGameQueue fetches a single queue by its ID.
func GetGameQueue(queueID string) (*GameQueue, error) {
	var q GameQueue
	if err := server.S.DB.First(&q, "id = ?", queueID).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

// GetGameQueuesForGame returns every queue for a game, ordered by
// created_at ASC (with id as tiebreaker for determinism). The first
// element is the default queue.
func GetGameQueuesForGame(gameID string) ([]GameQueue, error) {
	var queues []GameQueue
	err := server.S.DB.
		Where("game_id = ?", gameID).
		Order("created_at ASC, id ASC").
		Find(&queues).Error
	return queues, err
}

// GetDefaultQueueForGame returns the oldest queue belonging to gameID.
// Used when an API call (matchmaking, lobby, rating) does not specify a
// queueID. Returns gorm.ErrRecordNotFound if the game has no queues
// (which should be impossible after migration — every game gets a
// primary queue at creation time).
func GetDefaultQueueForGame(gameID string) (*GameQueue, error) {
	var q GameQueue
	err := server.S.DB.
		Where("game_id = ?", gameID).
		Order("created_at ASC, id ASC").
		First(&q).Error
	if err != nil {
		return nil, err
	}
	return &q, nil
}

// ResolveQueue returns the queue identified by queueID, or the game's
// default queue if queueID is empty. Verifies that the resolved queue
// belongs to gameID. Returns an error if queueID is non-empty but the
// queue belongs to a different game (prevents cross-game queue reuse).
func ResolveQueue(gameID, queueID string) (*GameQueue, error) {
	if queueID == "" {
		return GetDefaultQueueForGame(gameID)
	}
	q, err := GetGameQueue(queueID)
	if err != nil {
		return nil, err
	}
	if q.GameID != gameID {
		return nil, errors.New("queue does not belong to this game")
	}
	return q, nil
}

type UpdateGameQueueParams struct {
	Name                    string  `json:"name"`
	LobbyEnabled            *bool   `json:"lobby_enabled"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
	DefaultRating           int     `json:"default_rating"`
	KFactor                 int     `json:"k_factor"`
	MetadataEnabled         *bool   `json:"metadata_enabled"`
}

// applyQueueUpdate writes the non-zero fields from params onto q.
// Used by both UpdateGameQueue (single-queue update) and the legacy
// queue-field route in UpdateGame (which targets queues[0]).
func applyQueueUpdate(q *GameQueue, params UpdateGameQueueParams) error {
	if params.MatchmakingStrategy != "" && !slices.Contains(MATCHMAKING_STRATEGIES, params.MatchmakingStrategy) {
		return errors.New("invalid matchmaking strategy: " + params.MatchmakingStrategy + " must be one of " + strings.Join(MATCHMAKING_STRATEGIES, ", "))
	}
	if params.ELOStrategy != "" && !slices.Contains(ELO_STRATEGIES, params.ELOStrategy) {
		return errors.New("invalid elo strategy: " + params.ELOStrategy + " must be one of " + strings.Join(ELO_STRATEGIES, ", "))
	}
	if params.Name != "" {
		q.Name = params.Name
	}
	if params.LobbyEnabled != nil {
		q.LobbyEnabled = *params.LobbyEnabled
	}
	if params.LobbySize != 0 {
		q.LobbySize = params.LobbySize
	}
	if params.MatchmakingStrategy != "" {
		q.MatchmakingStrategy = params.MatchmakingStrategy
	}
	if params.MatchmakingMachineName != "" {
		q.MatchmakingMachineName = params.MatchmakingMachineName
	}
	if params.MatchmakingMachinePorts != nil {
		q.MatchmakingMachinePorts = params.MatchmakingMachinePorts
	}
	if params.ELOStrategy != "" {
		q.ELOStrategy = params.ELOStrategy
	}
	if params.DefaultRating != 0 {
		q.DefaultRating = params.DefaultRating
	}
	if params.KFactor != 0 {
		q.KFactor = params.KFactor
	}
	if params.MetadataEnabled != nil {
		q.MetadataEnabled = *params.MetadataEnabled
	}
	return nil
}

// UpdateGameQueue mutates the named queue. Caller must have verified
// game ownership before calling.
func UpdateGameQueue(queueID string, params UpdateGameQueueParams) (*GameQueue, error) {
	q, err := GetGameQueue(queueID)
	if err != nil {
		return nil, err
	}
	if err := applyQueueUpdate(q, params); err != nil {
		return nil, err
	}
	if err := server.S.DB.Save(q).Error; err != nil {
		return nil, err
	}
	return q, nil
}

// DeleteGameQueue removes the named queue. Refuses if it is the only
// remaining queue for the game (returns ErrLastQueue → 409). Caller
// must have verified game ownership before calling.
//
// Deleting the current default queue is allowed: the next-oldest queue
// silently becomes the default, since "default = oldest by created_at"
// is computed at lookup time.
//
// Cascades to ratings via ratings.game_queue_id ON DELETE CASCADE.
func DeleteGameQueue(queueID string) error {
	q, err := GetGameQueue(queueID)
	if err != nil {
		return err
	}
	var count int64
	if err := server.S.DB.Model(&GameQueue{}).
		Where("game_id = ?", q.GameID).
		Count(&count).Error; err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastQueue
	}
	return server.S.DB.Delete(&GameQueue{}, "id = ?", queueID).Error
}
