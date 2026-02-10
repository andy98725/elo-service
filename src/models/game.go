package models

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
)

const (
	MATCHMAKING_STRATEGY_RANDOM = "random"
	MATCHMAKING_STRATEGY_RATING = "rating"
	ELO_STRATEGY_UNRANKED       = "unranked"
	ELO_STRATEGY_CLASSIC        = "classic"
)

var MATCHMAKING_STRATEGIES = []string{MATCHMAKING_STRATEGY_RANDOM, MATCHMAKING_STRATEGY_RATING}
var ELO_STRATEGIES = []string{ELO_STRATEGY_UNRANKED, ELO_STRATEGY_CLASSIC}

type Game struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	OwnerID     string    `json:"owner_id" gorm:"not null"`
	Owner       User      `json:"owner" gorm:"foreignKey:OwnerID"`
	Name        string    `json:"name" gorm:"uniqueIndex;not null"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`

	// TODO separate into leaderboard if needed
	GuestsAllowed           bool          `json:"guests_allowed" gorm:"default:true"`
	PublicResults           bool          `json:"public_results" gorm:"default:false"`
	LobbySize               int           `json:"lobby_size" gorm:"default:2"`
	MatchmakingStrategy     string        `json:"matchmaking_strategy" gorm:"not null;default:'random'"`
	MatchmakingMachineName  string        `json:"matchmaking_machine_name" gorm:"not null"`
	MatchmakingSnapshotName string        `json:"matchmaking_snapshot_name" gorm:"not null"`
	MatchmakingMachinePorts pq.Int64Array `json:"matchmaking_machine_ports" gorm:"type:integer[];default:'{}'"`
	ELOStrategy             string        `json:"elo_strategy" gorm:"not null;default:'unranked'"`
	DefaultRating           int           `json:"default_rating" gorm:"default:1000"`
}

type GameResp struct {
	ID                      string  `json:"id"`
	OwnerID                 string  `json:"owner_id"`
	Name                    string  `json:"name"`
	Description             string  `json:"description"`
	GuestsAllowed           bool    `json:"guests_allowed"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
}

func (u *Game) ToResp() *GameResp {
	return &GameResp{
		ID:                      u.ID,
		OwnerID:                 u.OwnerID,
		Name:                    u.Name,
		Description:             u.Description,
		GuestsAllowed:           u.GuestsAllowed,
		LobbySize:               u.LobbySize,
		MatchmakingStrategy:     u.MatchmakingStrategy,
		MatchmakingMachineName:  u.MatchmakingMachineName,
		MatchmakingMachinePorts: []int64(u.MatchmakingMachinePorts),
		ELOStrategy:             u.ELOStrategy,
	}
}

type CreateGameParams struct {
	Name                    string
	Description             string
	GuestsAllowed           bool
	PublicResults           bool
	LobbySize               int
	MatchmakingStrategy     string
	MatchmakingMachineName  string
	MatchmakingMachinePorts []int64
	ELOStrategy             string
}

func CreateGame(params CreateGameParams, owner User) (*Game, error) {
	if params.MatchmakingMachineName == "" {
		params.MatchmakingMachineName = "docker.io/andy98725/example-server:latest"
	}
	if params.MatchmakingStrategy == "" {
		params.MatchmakingStrategy = MATCHMAKING_STRATEGY_RANDOM
	}
	if !slices.Contains(MATCHMAKING_STRATEGIES, params.MatchmakingStrategy) {
		return nil, errors.New("invalid matchmaking strategy: " + params.MatchmakingStrategy + " must be one of " + strings.Join(MATCHMAKING_STRATEGIES, ", "))
	}
	if params.ELOStrategy == "" {
		params.ELOStrategy = ELO_STRATEGY_UNRANKED
	}
	if !slices.Contains(ELO_STRATEGIES, params.ELOStrategy) {
		return nil, errors.New("invalid elo strategy: " + params.ELOStrategy + " must be one of " + strings.Join(ELO_STRATEGIES, ", "))
	}

	game := &Game{
		OwnerID:                 owner.ID,
		Owner:                   owner,
		Name:                    params.Name,
		Description:             params.Description,
		GuestsAllowed:           params.GuestsAllowed,
		PublicResults:           params.PublicResults,
		LobbySize:               params.LobbySize,
		MatchmakingStrategy:     params.MatchmakingStrategy,
		MatchmakingMachineName:  params.MatchmakingMachineName,
		MatchmakingMachinePorts: pq.Int64Array(params.MatchmakingMachinePorts),
		ELOStrategy:             params.ELOStrategy,
	}

	result := server.S.DB.Create(game)
	if result.Error != nil {
		return nil, result.Error
	}

	return game, nil
}

func GetGames(page, pageSize int) ([]Game, int, error) {
	var games []Game
	offset := page * pageSize
	result := server.S.DB.Offset(offset).Limit(pageSize).Find(&games)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return games, nextPage, result.Error
}
func GetGamesOfUser(page, pageSize int, userId string) ([]Game, int, error) {
	var games []Game
	offset := page * pageSize
	result := server.S.DB.Offset(offset).Limit(pageSize).Where("owner_id = ?", userId).Find(&games)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return games, nextPage, result.Error
}

func GetGame(id string) (*Game, error) {
	var game Game
	result := server.S.DB.First(&game, "id = ?", id)
	return &game, result.Error
}

type UpdateGameParams struct {
	Name                    string  `json:"name"`
	Description             string  `json:"description"`
	GuestsAllowed           *bool   `json:"guests_allowed"`
	LobbySize               int     `json:"lobby_size"`
	MatchmakingStrategy     string  `json:"matchmaking_strategy"`
	MatchmakingMachineName  string  `json:"matchmaking_machine_name"`
	MatchmakingMachinePorts []int64 `json:"matchmaking_machine_ports"`
	ELOStrategy             string  `json:"elo_strategy"`
}

func UpdateGame(id string, params UpdateGameParams, owner User) (*Game, error) {
	if params.MatchmakingStrategy != "" && !slices.Contains(MATCHMAKING_STRATEGIES, params.MatchmakingStrategy) {
		return nil, errors.New("invalid matchmaking strategy: " + params.MatchmakingStrategy + " must be one of " + strings.Join(MATCHMAKING_STRATEGIES, ", "))
	}
	if params.ELOStrategy != "" && !slices.Contains(ELO_STRATEGIES, params.ELOStrategy) {
		return nil, errors.New("invalid elo strategy: " + params.ELOStrategy + " must be one of " + strings.Join(ELO_STRATEGIES, ", "))
	}

	game, err := GetGame(id)
	if err != nil {
		return nil, err
	}
	if game.OwnerID != owner.ID {
		return nil, errors.New("you are not the owner of this game")
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
	if params.LobbySize != 0 {
		game.LobbySize = params.LobbySize
	}
	if params.MatchmakingStrategy != "" {
		game.MatchmakingStrategy = params.MatchmakingStrategy
	}
	if params.MatchmakingMachineName != "" {
		game.MatchmakingMachineName = params.MatchmakingMachineName
	}
	if params.MatchmakingMachinePorts != nil {
		game.MatchmakingMachinePorts = params.MatchmakingMachinePorts
	}
	if params.ELOStrategy != "" {
		game.ELOStrategy = params.ELOStrategy
	}

	result := server.S.DB.Save(game)
	if result.Error != nil {
		return nil, result.Error
	}

	return game, nil
}
func SetGameSnapshot(id string, snapshotName string) error {
	game, err := GetGame(id)
	if err != nil {
		return err
	}
	game.MatchmakingSnapshotName = snapshotName
	return server.S.DB.Save(game).Error
}

func DeleteGame(id string, owner User) error {
	game, err := GetGame(id)
	if err != nil {
		return err
	}
	if game.OwnerID != owner.ID {
		return errors.New("you are not the owner of this game")
	}

	result := server.S.DB.Delete(game)
	if result.Error != nil {
		return result.Error
	}

	return nil
}
