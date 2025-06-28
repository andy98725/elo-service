package models

import (
	"time"

	"github.com/andy98725/elo-service/src/server"
	"gorm.io/gorm"
)

type MatchResult struct {
	ID        string    `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`
	GameID    string    `json:"game_id" gorm:"not null"`
	Game      Game      `json:"game" gorm:"foreignKey:GameID"`
	Players   []User    `json:"players" gorm:"many2many:match_result_players;"`
	WinnerID  string    `json:"winner_id"`
	Winner    User      `json:"winner" gorm:"foreignKey:WinnerID"`
	Result    string    `json:"result" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type MatchResultResp struct {
	ID       string     `json:"id"`
	GameID   string     `json:"game_id"`
	Players  []UserResp `json:"players"`
	WinnerID string     `json:"winner_id"`
	Result   string     `json:"result"`
}

func (m *MatchResult) ToResp() *MatchResultResp {
	playersResp := make([]UserResp, len(m.Players))
	for i, player := range m.Players {
		playersResp[i] = *player.ToResp()
	}

	return &MatchResultResp{
		ID:       m.ID,
		GameID:   m.GameID,
		Players:  playersResp,
		WinnerID: m.WinnerID,
		Result:   m.Result,
	}
}

func MatchEnded(matchID string, winnerID string, result string) (*MatchResult, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return nil, err
	}

	matchResult := &MatchResult{
		GameID:   match.GameID,
		Players:  match.Players,
		WinnerID: winnerID,
		Result:   result,
	}

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
	result := server.S.DB.Preload("Players").First(&matchResult, "id = ?", matchID)
	if result.Error != nil {
		return nil, result.Error
	}
	return &matchResult, nil
}

func GetMatchResultsOfGame(gameID string, page, pageSize int) ([]MatchResult, int, error) {
	var matchResults []MatchResult
	offset := page * pageSize
	result := server.S.DB.Preload("Players").Offset(offset).Limit(pageSize).Find(&matchResults, "game_id = ?", gameID)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matchResults, nextPage, nil
}

func GetMatchResults(page, pageSize int) ([]MatchResult, int, error) {
	var matchResults []MatchResult
	offset := page * pageSize
	result := server.S.DB.Preload("Players").Offset(offset).Limit(pageSize).Find(&matchResults)
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return matchResults, nextPage, nil
}
