package models

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
)

type Match struct {
	ID          string         `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID      string         `json:"game_id" gorm:"not null"`
	Game        Game           `json:"game" gorm:"foreignKey:GameID"`
	MachineName string         `json:"machine_name" gorm:""`
	Players     []User         `json:"players" gorm:"many2many:match_players;"`
	GuestIDs    pq.StringArray `json:"guest_ids" gorm:"type:text[];default:'{}'"`
	AuthCode    string         `json:"auth_code" gorm:"not null"`
	Status      string         `json:"status" gorm:"not null"`
	CreatedAt   time.Time      `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time      `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResp struct {
	ID          string     `json:"id"`
	GameID      string     `json:"game_id"`
	MachineName string     `json:"machine_name"`
	Players     []UserResp `json:"players"`
	GuestIDs    []string   `json:"guest_ids"`
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

func (m *Match) ConnectionAddress() string {
	httpURL := fmt.Sprintf("https://%s.fly.dev:8443/%s/{port}/", server.S.Config.FlyAppName, m.MachineName)
	tcpURL := fmt.Sprintf("tcp://%s.fly.dev:8082", server.S.Config.FlyAppName)
	tcpMessage := fmt.Sprintf("%s:{port}", m.MachineName)

	return `{"http": "` + httpURL + `", "tcpURL": "` + tcpURL + `", "tcpMessage": "` + tcpMessage + `"}`
}

func MatchStarted(gameID string, connInfo *server.MachineConnectionInfo, playerIDs []string) (*Match, error) {
	var users []User
	var guestIDs []string

	// Split players and guests based on ID prefix
	for _, playerID := range playerIDs {
		if len(playerID) > 2 && playerID[:2] == "g_" {
			guestIDs = append(guestIDs, playerID)
		} else {
			users = append(users, User{ID: playerID})
		}
	}

	match := &Match{
		GameID:      gameID,
		Players:     users,
		GuestIDs:    guestIDs,
		MachineName: connInfo.MachineName,
		AuthCode:    connInfo.AuthCode,
		Status:      "started",
	}

	result := server.S.DB.Create(match)
	if result.Error != nil {
		return nil, result.Error
	}

	slog.Info("Match started", "gameID", gameID, "connInfo", connInfo, "playerIDs", playerIDs)
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

func GetMatchByTokenID(tokenID string) (*Match, error) {
	var match Match
	result := server.S.DB.Preload("Players").First(&match, "auth_code = ?", tokenID)
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
