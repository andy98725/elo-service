package models

import (
	"log/slog"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

type MatchResult struct {
	ID        string         `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID    string         `json:"game_id" gorm:"not null"`
	Game      Game           `json:"game" gorm:"foreignKey:GameID"`
	Players   []User         `json:"players" gorm:"many2many:match_result_players;"`
	GuestIDs  pq.StringArray `json:"guest_ids" gorm:"type:text[];default:'{}'"`
	WinnerIDs pq.StringArray `json:"winner_ids" gorm:"type:text[];default:'{}'"`
	Result    string         `json:"result" gorm:"not null"`
	LogsKey   string         `json:"logs_key"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResultResp struct {
	ID        string     `json:"id"`
	GameID    string     `json:"game_id"`
	Players   []UserResp `json:"players"`
	GuestIDs  []string   `json:"guest_ids"`
	WinnerIDs []string   `json:"winner_ids"`
	Result    string     `json:"result"`
}

func (m *MatchResult) ToResp() *MatchResultResp {
	playersResp := make([]UserResp, len(m.Players))
	for i, player := range m.Players {
		playersResp[i] = *player.ToResp()
	}

	return &MatchResultResp{
		ID:        m.ID,
		GameID:    m.GameID,
		Players:   playersResp,
		GuestIDs:  m.GuestIDs,
		WinnerIDs: m.WinnerIDs,
		Result:    m.Result,
	}
}

func MatchEnded(matchID string, winnerIDs []string, result string, logsKey string) (*MatchResult, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return nil, err
	}

	matchResult := &MatchResult{
		GameID:    match.GameID,
		Players:   match.Players,
		GuestIDs:  match.GuestIDs,
		WinnerIDs: winnerIDs,
		Result:    result,
		LogsKey:   logsKey,
	}
	slog.Info("Match ended", "matchID", matchID, "winnerIDs", winnerIDs, "result", result, "logsKey", logsKey)

	// Report result and delete match in one transaction
	err = server.S.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(matchResult).Error; err != nil {
			return err
		}

		if err := tx.Delete(&Match{ID: matchID}).Error; err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return matchResult, nil
}

func GetMatchResult(matchID string) (*MatchResult, error) {
	var matchResult MatchResult
	result := server.S.DB.Preload("Game").Preload("Players").First(&matchResult, "id = ?", matchID)
	if result.Error != nil {
		return nil, result.Error
	}
	return &matchResult, nil
}

func GetMatchResultsOfGame(gameID string, page, pageSize int) ([]MatchResult, int, error) {
	var matchResults []MatchResult
	offset := page * pageSize
	result := server.S.DB.Preload("Game").Preload("Players").Offset(offset).Limit(pageSize).Find(&matchResults, "game_id = ?", gameID)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matchResults, nextPage, nil
}

func GetMatchResultsOfPlayer(playerID string, page, pageSize int) ([]MatchResult, int, error) {
	var matchResults []MatchResult
	offset := page * pageSize

	q := server.S.DB.Preload("Game").Preload("Players").Offset(offset).Limit(pageSize)
	if len(playerID) > 2 && playerID[:2] == "g_" {
		q = q.Where("? = ANY(guest_ids)", playerID)
	} else {
		q = q.Where("? = players @> ARRAY[?]::uuid[]", playerID)
	}
	result := q.Find(&matchResults)

	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matchResults, nextPage, nil
}

func GetMatchResults(page, pageSize int) ([]MatchResult, int, error) {
	var matchResults []MatchResult
	offset := page * pageSize
	result := server.S.DB.Preload("Game").Preload("Players").Offset(offset).Limit(pageSize).Find(&matchResults)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matchResults, nextPage, nil
}

func CanUserSeeMatchResult(userID string, matchResultID string) (bool, error) {
	matchResult, err := GetMatchResult(matchResultID)
	if err != nil {
		return false, err
	}
	if matchResult.Game.PublicResults {
		return true, nil
	}

	// Admin can see all match results
	if user, err := GetById(userID); err == nil && user.IsAdmin {
		return true, nil
	}

	// If user is owner of game, they can see all match results
	if matchResult.Game.OwnerID == userID {
		return true, nil
	}
	// If user is a player or guest in the match, they can see the match result
	for _, player := range matchResult.Players {
		if player.ID == userID {
			return true, nil
		}
	}
	for _, guestID := range matchResult.GuestIDs {
		if guestID == userID {
			return true, nil
		}
	}

	return false, nil
}

func IsUserMatchResultAdmin(userID string, matchResultID string) (bool, error) {
	if util.IsGuestID(userID) {
		return false, nil
	}

	user, err := GetById(userID)
	if err != nil {
		return false, err
	}
	if user.IsAdmin {
		return true, nil
	}

	matchResult, err := GetMatchResult(matchResultID)
	if err != nil {
		return false, err
	}
	if matchResult.Game.OwnerID == userID {
		return true, nil
	}

	return false, nil
}
