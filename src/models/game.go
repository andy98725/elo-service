package models

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/server"
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
	GuestsAllowed          bool   `json:"guests_allowed" gorm:"default:true"`
	MatchmakingStrategy    string `json:"matchmaking_strategy" gorm:"not null"`
	MatchmakingMachineName string `json:"matchmaking_machine_name" gorm:"not null"`
	ELOStrategy            string `json:"elo_strategy" gorm:"not null"`
}

type GameResp struct {
	ID                     string `json:"id"`
	OwnerID                string `json:"owner_id"`
	Name                   string `json:"name"`
	Description            string `json:"description"`
	GuestsAllowed          bool   `json:"guests_allowed"`
	MatchmakingStrategy    string `json:"matchmaking_strategy"`
	MatchmakingMachineName string `json:"matchmaking_machine_name"`
	ELOStrategy            string `json:"elo_strategy"`
}

func (u *Game) ToResp() *GameResp {
	return &GameResp{
		ID:                     u.ID,
		OwnerID:                u.OwnerID,
		Name:                   u.Name,
		Description:            u.Description,
		GuestsAllowed:          u.GuestsAllowed,
		MatchmakingStrategy:    u.MatchmakingStrategy,
		MatchmakingMachineName: u.MatchmakingMachineName,
		ELOStrategy:            u.ELOStrategy,
	}
}

type CreateGameParams struct {
	Name                   string
	Description            string
	GuestsAllowed          bool
	MatchmakingStrategy    string
	MatchmakingMachineName string
	ELOStrategy            string
}

func CreateGame(params CreateGameParams, owner User) (*Game, error) {
	if !slices.Contains(MATCHMAKING_STRATEGIES, params.MatchmakingStrategy) {
		return nil, errors.New("invalid matchmaking strategy: " + params.MatchmakingStrategy + " must be one of " + strings.Join(MATCHMAKING_STRATEGIES, ", "))
	}
	if !slices.Contains(ELO_STRATEGIES, params.ELOStrategy) {
		return nil, errors.New("invalid elo strategy: " + params.ELOStrategy + " must be one of " + strings.Join(ELO_STRATEGIES, ", "))
	}

	game := &Game{
		OwnerID:                owner.ID,
		Owner:                  owner,
		Name:                   params.Name,
		Description:            params.Description,
		GuestsAllowed:          params.GuestsAllowed,
		MatchmakingStrategy:    params.MatchmakingStrategy,
		MatchmakingMachineName: params.MatchmakingMachineName,
		ELOStrategy:            params.ELOStrategy,
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
