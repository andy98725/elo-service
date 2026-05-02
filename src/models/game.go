package models

import (
	"errors"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"gorm.io/gorm"
)

const (
	MATCHMAKING_STRATEGY_RANDOM = "random"
	MATCHMAKING_STRATEGY_RATING = "rating"
	ELO_STRATEGY_UNRANKED       = "unranked"
	ELO_STRATEGY_CLASSIC        = "classic"
)

var MATCHMAKING_STRATEGIES = []string{MATCHMAKING_STRATEGY_RANDOM, MATCHMAKING_STRATEGY_RATING}

// ErrNotGameOwner is returned by mutation operations when the caller is not
// the owner of the target game. Handlers should map this to HTTP 403.
var ErrNotGameOwner = errors.New("not the owner of this game")
var ELO_STRATEGIES = []string{ELO_STRATEGY_UNRANKED, ELO_STRATEGY_CLASSIC}

// Game holds identity and game-wide policy. Per-pool matchmaking knobs
// (image, ports, lobby size, ELO strategy, etc.) live on GameQueue —
// one Game has 1..N queues. The default queue is queues[0] (oldest by
// created_at, id), used when API callers don't specify a queueID.
type Game struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OwnerID     string    `json:"owner_id" gorm:"not null"`
	Owner       User      `json:"owner" gorm:"foreignKey:OwnerID"`
	Name        string    `json:"name" gorm:"uniqueIndex;not null"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`

	// Game-wide policy knobs. Defaults are enforced in CreateGame, not via
	// `default:true` GORM tags — see the explanation on those fields and
	// the same trap on GameQueue.
	GuestsAllowed   bool `json:"guests_allowed"`
	PublicResults   bool `json:"public_results"`
	PublicMatchLogs bool `json:"public_match_logs" gorm:"default:false"`
	// SpectateEnabled lets non-participants discover live matches in this
	// game and tail the per-match spectator stream. Opt-in (default false).
	// Distinct from PublicMatchLogs, which is post-game container stdout —
	// the spectator stream is its own pipe written by the game server to
	// /shared/spectate.stream and uploaded as chunked S3 objects.
	SpectateEnabled bool `json:"spectate_enabled" gorm:"default:false"`

	// Queues is the ordered list of matchmaking pools for this game.
	// Always non-empty after creation: CreateGame inserts a primary queue
	// in the same transaction. Order is created_at ASC (id tiebreaker),
	// so Queues[0] is the default queue.
	Queues []GameQueue `json:"queues" gorm:"foreignKey:GameID;constraint:OnDelete:CASCADE"`
}

// GameResp serializes a game with its queues. Per-queue config
// (lobby_size, matchmaking_machine_name, etc.) lives under `queues[]` —
// `queues[0]` is the default queue, used when API callers don't specify
// a queueID.
type GameResp struct {
	ID              string          `json:"id"`
	Owner           UserResp        `json:"owner"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	GuestsAllowed   bool            `json:"guests_allowed"`
	PublicResults   bool            `json:"public_results"`
	PublicMatchLogs bool            `json:"public_match_logs"`
	SpectateEnabled bool            `json:"spectate_enabled"`
	Queues          []GameQueueResp `json:"queues"`
}

func (g *Game) ToResp() *GameResp {
	queues := make([]GameQueueResp, len(g.Queues))
	for i, q := range g.Queues {
		queues[i] = *q.ToResp()
	}
	return &GameResp{
		ID:              g.ID,
		Owner:           *g.Owner.ToResp(),
		Name:            g.Name,
		Description:     g.Description,
		GuestsAllowed:   g.GuestsAllowed,
		PublicResults:   g.PublicResults,
		PublicMatchLogs: g.PublicMatchLogs,
		SpectateEnabled: g.SpectateEnabled,
		Queues:          queues,
	}
}

// CreateGameParams bundles game-level fields and the parameters for the
// primary queue created alongside the game. Existing API clients pass the
// queue fields flat (lobby_size, matchmaking_machine_name, etc.) — the
// handler folds those into Queue before calling CreateGame.
type CreateGameParams struct {
	Name            string
	Description     string
	GuestsAllowed   *bool
	PublicResults   *bool
	PublicMatchLogs *bool
	SpectateEnabled *bool

	// PrimaryQueue holds the matchmaking config for the auto-created
	// default queue. The handler is expected to populate this from the
	// flat queue-field payload submitted by existing clients.
	PrimaryQueue CreateGameQueueParams
}

// CreateGame inserts the Game and its primary queue in a single transaction.
// The primary queue is named "primary" by default and seeded with whatever
// matchmaking config the caller passed via params.PrimaryQueue.
func CreateGame(params CreateGameParams, owner User) (*Game, error) {
	if err := applyQueueDefaults(&params.PrimaryQueue); err != nil {
		return nil, err
	}

	guestsAllowed := true
	if params.GuestsAllowed != nil {
		guestsAllowed = *params.GuestsAllowed
	}
	publicResults := true
	if params.PublicResults != nil {
		publicResults = *params.PublicResults
	}
	publicMatchLogs := false
	if params.PublicMatchLogs != nil {
		publicMatchLogs = *params.PublicMatchLogs
	}
	spectateEnabled := false
	if params.SpectateEnabled != nil {
		spectateEnabled = *params.SpectateEnabled
	}

	game := &Game{
		OwnerID:         owner.ID,
		Owner:           owner,
		Name:            params.Name,
		Description:     params.Description,
		GuestsAllowed:   guestsAllowed,
		PublicResults:   publicResults,
		PublicMatchLogs: publicMatchLogs,
		SpectateEnabled: spectateEnabled,
	}

	queue := queueFromParams(params.PrimaryQueue)

	err := server.S.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(game).Error; err != nil {
			return err
		}
		queue.GameID = game.ID
		if err := tx.Create(queue).Error; err != nil {
			return err
		}
		game.Queues = []GameQueue{*queue}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return game, nil
}

// gameQuery preloads queues in their canonical order (default = Queues[0]).
// Owner is preloaded so GameResp.Owner serializes with the real user; without
// it, GORM leaves the embedded User struct zero-valued and the JSON response
// shows owner.id="" / username="" / email="".
func gameQuery() *gorm.DB {
	return server.S.DB.Preload("Owner").Preload("Queues", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC, id ASC")
	})
}

func GetGames(page, pageSize int) ([]Game, int, error) {
	var games []Game
	offset := page * pageSize
	result := gameQuery().Offset(offset).Limit(pageSize).Find(&games)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return games, nextPage, result.Error
}

func GetGamesOfUser(page, pageSize int, userId string) ([]Game, int, error) {
	var games []Game
	offset := page * pageSize
	result := gameQuery().Offset(offset).Limit(pageSize).Where("owner_id = ?", userId).Find(&games)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return games, nextPage, result.Error
}

func GetGame(id string) (*Game, error) {
	var game Game
	result := gameQuery().First(&game, "id = ?", id)
	return &game, result.Error
}

// UpdateGameParams covers game-level fields plus the legacy flat queue
// fields. Any non-zero queue field is applied to the default queue
// (Queues[0]) — the same backwards-compat shim that lets existing
// CreateGame clients keep working without code changes.
type UpdateGameParams struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	GuestsAllowed   *bool  `json:"guests_allowed"`
	PublicResults   *bool  `json:"public_results"`
	PublicMatchLogs *bool  `json:"public_match_logs"`
	SpectateEnabled *bool  `json:"spectate_enabled"`

	// Legacy flat queue fields. Applied to the game's default queue.
	// Multi-queue clients should hit /game/:id/queue/:queueID directly.
	LobbyEnabled            *bool   `json:"lobby_enabled"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
	KFactor                 int     `json:"k_factor"`
	MetadataEnabled         *bool   `json:"metadata_enabled"`
}

// hasQueueFieldUpdates reports whether any of the legacy flat queue
// fields on UpdateGameParams were set.
func (p *UpdateGameParams) hasQueueFieldUpdates() bool {
	return p.LobbyEnabled != nil ||
		p.LobbySize != 0 ||
		p.MatchmakingStrategy != "" ||
		p.MatchmakingMachineName != "" ||
		p.MatchmakingMachinePorts != nil ||
		p.ELOStrategy != "" ||
		p.KFactor != 0 ||
		p.MetadataEnabled != nil
}

// toQueueUpdate folds legacy flat queue fields into an
// UpdateGameQueueParams suitable for applyQueueUpdate.
func (p *UpdateGameParams) toQueueUpdate() UpdateGameQueueParams {
	return UpdateGameQueueParams{
		LobbyEnabled:            p.LobbyEnabled,
		LobbySize:               p.LobbySize,
		MatchmakingStrategy:     p.MatchmakingStrategy,
		MatchmakingMachineName:  p.MatchmakingMachineName,
		MatchmakingMachinePorts: p.MatchmakingMachinePorts,
		ELOStrategy:             p.ELOStrategy,
		KFactor:                 p.KFactor,
		MetadataEnabled:         p.MetadataEnabled,
	}
}

// UpdateGame applies game-level updates and, if any legacy queue fields
// are set, applies them to the default queue (Queues[0]) in the same
// transaction.
func UpdateGame(id string, params UpdateGameParams, owner User) (*Game, error) {
	game, err := GetGame(id)
	if err != nil {
		return nil, err
	}
	if game.OwnerID != owner.ID {
		return nil, ErrNotGameOwner
	}

	if params.Name != "" {
		game.Name = params.Name
	}
	if params.Description != "" {
		game.Description = params.Description
	}
	if params.GuestsAllowed != nil {
		game.GuestsAllowed = *params.GuestsAllowed
	}
	if params.PublicResults != nil {
		game.PublicResults = *params.PublicResults
	}
	if params.PublicMatchLogs != nil {
		game.PublicMatchLogs = *params.PublicMatchLogs
	}
	if params.SpectateEnabled != nil {
		game.SpectateEnabled = *params.SpectateEnabled
	}

	err = server.S.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(game).Error; err != nil {
			return err
		}
		if params.hasQueueFieldUpdates() {
			if len(game.Queues) == 0 {
				return errors.New("game has no queues to update")
			}
			defaultQueue := game.Queues[0]
			qu := params.toQueueUpdate()
			if err := applyQueueUpdate(&defaultQueue, qu); err != nil {
				return err
			}
			if err := tx.Save(&defaultQueue).Error; err != nil {
				return err
			}
			game.Queues[0] = defaultQueue
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return game, nil
}

// DeleteGame removes the game and cascades to its queues (and through
// them, its ratings via game_queues → ratings ON DELETE CASCADE).
func DeleteGame(id string, owner User) error {
	game, err := GetGame(id)
	if err != nil {
		return err
	}
	if game.OwnerID != owner.ID {
		return ErrNotGameOwner
	}

	result := server.S.DB.Delete(game)
	if result.Error != nil {
		return result.Error
	}

	return nil
}
