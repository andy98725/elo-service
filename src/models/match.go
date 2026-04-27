package models

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type Match struct {
	ID               string         `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID           string         `json:"game_id" gorm:"not null"`
	Game             Game           `json:"game" gorm:"foreignKey:GameID"`
	ServerInstanceID string         `json:"server_instance_id" gorm:"index"`
	ServerInstance   ServerInstance `json:"server_instance" gorm:"foreignKey:ServerInstanceID"`
	Players          []User         `json:"players" gorm:"many2many:match_players;"`
	GuestIDs         pq.StringArray `json:"guest_ids" gorm:"type:text[];default:'{}'"`
	AuthCode         string         `json:"auth_code" gorm:"not null"`
	Status           string         `json:"status" gorm:"not null"`
	CreatedAt        time.Time      `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt        time.Time      `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResp struct {
	ID             string     `json:"id"`
	GameID         string     `json:"game_id"`
	ServerAddress  string     `json:"server_address"`
	Players        []UserResp `json:"players"`
	GuestIDs       []string   `json:"guest_ids"`
	Status         string     `json:"status"`
}

func (m *Match) ToResp() *MatchResp {
	players := make([]UserResp, len(m.Players))
	for i, player := range m.Players {
		players[i] = *player.ToResp()
	}

	return &MatchResp{
		ID:            m.ID,
		GameID:        m.GameID,
		ServerAddress: m.ConnectionAddress(),
		Players:       players,
		GuestIDs:      m.GuestIDs,
		Status:        m.Status,
	}
}

func (m *Match) ConnectionAddress() string {
	if len(m.ServerInstance.HostPorts) > 0 {
		return fmt.Sprintf("%s:%d", m.ServerInstance.MachineHost.PublicIP, m.ServerInstance.HostPorts[0])
	}
	return m.ServerInstance.MachineHost.PublicIP
}

func MatchStarted(gameID string, serverInstanceID string, authCode string, playerIDs []string) (*Match, error) {
	var users []User
	var guestIDs []string

	for _, playerID := range playerIDs {
		if len(playerID) > 2 && playerID[:2] == "g_" {
			guestIDs = append(guestIDs, playerID)
		} else {
			users = append(users, User{ID: playerID})
		}
	}

	match := &Match{
		GameID:           gameID,
		ServerInstanceID: serverInstanceID,
		Players:          users,
		GuestIDs:         guestIDs,
		AuthCode:         authCode,
		Status:           "started",
	}

	if err := server.S.DB.Create(match).Error; err != nil {
		return nil, err
	}

	slog.Info("Match started", "gameID", gameID, "serverInstanceID", serverInstanceID, "playerIDs", playerIDs)
	return match, nil
}

func matchQuery() *gorm.DB {
	return server.S.DB.Preload("ServerInstance.MachineHost").Preload("Game").Preload("Players")
}

func GetMatch(matchID string) (*Match, error) {
	var match Match
	result := matchQuery().First(&match, "id = ?", matchID)
	return &match, result.Error
}

func GetMatchByTokenID(tokenID string) (*Match, error) {
	var match Match
	result := matchQuery().First(&match, "auth_code = ?", tokenID)
	return &match, result.Error
}

func GetMatchesOfGame(gameID string, page, pageSize int) ([]Match, int, error) {
	var matches []Match
	offset := page * pageSize
	result := matchQuery().Offset(offset).Limit(pageSize).Find(&matches, "game_id = ?", gameID)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matches, nextPage, result.Error
}

func GetMatches(page, pageSize int) ([]Match, int, error) {
	var matches []Match
	offset := page * pageSize
	result := matchQuery().Offset(offset).Limit(pageSize).Find(&matches)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matches, nextPage, result.Error
}

func GetMatchesUnderway(page, pageSize int) ([]Match, int, error) {
	var matches []Match
	offset := page * pageSize
	result := matchQuery().Offset(offset).Limit(pageSize).Find(&matches, "status = ?", "started")
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matches, nextPage, result.Error
}

func IsMatchUnderway(matchID string) (bool, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return false, err
	}
	return match.Status == "started", nil
}

func CanUserSeeMatch(userID string, matchID string) (bool, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return false, err
	}

	if user, err := GetById(userID); err == nil && user.IsAdmin {
		return true, nil
	}

	if match.Game.OwnerID == userID {
		return true, nil
	}

	for _, player := range match.Players {
		if player.ID == userID {
			return true, nil
		}
	}
	for _, guestID := range match.GuestIDs {
		if guestID == userID {
			return true, nil
		}
	}

	return false, nil
}
