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
	PlayerIDs []string  `json:"player_ids" gorm:"not null"`
	Players   []User    `json:"players" gorm:"many2many:match_players;"`
	WinnerID  string    `json:"winner_id"`
	Winner    User      `json:"winner" gorm:"foreignKey:WinnerID"`
	Result    string    `json:"result" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

func MatchEnded(matchID string, winnerID string) (*MatchResult, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return nil, err
	}

	resultTxt := "win"
	if winnerID == "" {
		resultTxt = "draw"
	}

	matchResult := &MatchResult{
		GameID:    match.GameID,
		PlayerIDs: match.PlayerIDs,
		WinnerID:  winnerID,
		Result:    resultTxt,
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
