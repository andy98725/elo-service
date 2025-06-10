package models

import (
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
)

type Match struct {
	ID          string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID      string    `json:"game_id" gorm:"not null"`
	Game        Game      `json:"game" gorm:"foreignKey:GameID"`
	MachineName string    `json:"machine_name" gorm:"not null"`
	Players     []User    `json:"players" gorm:"many2many:match_players;"`
	AuthCode    string    `json:"auth_code" gorm:"not null"`
	Status      string    `json:"status" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResp struct {
	ID          string     `json:"id"`
	GameID      string     `json:"game_id"`
	MachineName string     `json:"machine_name"`
	Players     []UserResp `json:"players"`
	Status      string     `json:"status"`
}

func (m *Match) ToResp() *MatchResp {
	players := make([]UserResp, len(m.Players))
	for i, player := range m.Players {
		players[i] = *player.ToResp()
	}

	return &MatchResp{
		ID:          m.ID,
		GameID:      m.GameID,
		MachineName: m.MachineName,
		Players:     players,
		Status:      m.Status,
	}
}
func (m *Match) ConnectionInfo() string {
	return "TODO: Match found!"
}

func MatchStarted(gameID string, machineName string, authCode string, playerIDs []string) (*Match, error) {
	slog.Debug("Match started", "gameID", gameID, "machineName", machineName, "authCode", authCode, "playerIDs", playerIDs)

	players := make([]User, len(playerIDs))
	for i, playerID := range playerIDs {
		players[i] = User{ID: playerID}
	}
	match := &Match{
		GameID:      gameID,
		MachineName: machineName,
		Players:     players,
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
	result := server.S.DB.Preload("Players").First(&match, "id = ?", matchID)
	if result.Error != nil {
		return nil, result.Error
	}
	return &match, nil
}

func GetMatchesOfGame(gameID string, page, pageSize int) ([]Match, int, error) {
	var matches []Match
	offset := page * pageSize
	result := server.S.DB.Preload("Players").Offset(offset).Limit(pageSize).Find(&matches, "game_id = ?", gameID)
	if result.Error != nil {
		return nil, -1, result.Error
	}

	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matches, nextPage, nil
}

func GetMatches(page, pageSize int) ([]Match, int, error) {
	var matches []Match
	offset := page * pageSize
	result := server.S.DB.Preload("Players").Offset(offset).Limit(pageSize).Find(&matches)
	if result.Error != nil {
		return nil, -1, result.Error
	}

	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matches, nextPage, nil
}
