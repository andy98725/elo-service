package models

import (
	"time"

	"github.com/andy98725/elo-service/src/server"
)

type Match struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID      string    `json:"game_id" gorm:"not null"`
	Game        Game      `json:"game" gorm:"foreignKey:GameID"`
	MachineName string    `json:"machine_name" gorm:"not null"`
	PlayerIDs   []string  `json:"player_ids" gorm:"not null"`
	Players     []User    `json:"players" gorm:"many2many:match_players;"`
	AuthCode    string    `json:"auth_code" gorm:"not null"`
	Status      string    `json:"status" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResp struct {
	ID          string   `json:"id"`
	GameID      string   `json:"game_id"`
	MachineName string   `json:"machine_name"`
	PlayerIDs   []string `json:"player_ids"`
	Status      string   `json:"status"`
}

func (m *Match) ToResp() *MatchResp {
	return &MatchResp{
		ID:          m.ID,
		GameID:      m.GameID,
		MachineName: m.MachineName,
		PlayerIDs:   m.PlayerIDs,
		Status:      m.Status,
	}
}

func MatchStarted(gameID string, machineName string, authCode string, playerIDs []string) (*Match, error) {
	match := &Match{
		GameID:      gameID,
		MachineName: machineName,
		PlayerIDs:   playerIDs,
		AuthCode:    authCode,
		Status:      "started",
	}

	result := server.S.DB.Create(match)
	if result.Error != nil {
		return nil, result.Error
	}

	return match, nil
}

func GetMatch(matchID string) (*Match, error) {
	var match Match
	result := server.S.DB.First(&match, "id = ?", matchID)
	if result.Error != nil {
		return nil, result.Error
	}
	return &match, nil
}

func GetMatchesOfGame(gameID string) ([]Match, error) {
	var matches []Match
	result := server.S.DB.Find(&matches, "game_id = ?", gameID)
	if result.Error != nil {
		return nil, result.Error
	}
	return matches, nil
}
