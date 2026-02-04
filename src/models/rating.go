package models

import (
	"errors"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"gorm.io/gorm"
)

type Rating struct {
	PlayerID  string    `json:"player_id" gorm:"primaryKey"`
	Player    User      `json:"player" gorm:"foreignKey:PlayerID"`
	GameID    string    `json:"game_id" gorm:"primaryKey"`
	Game      Game      `json:"game" gorm:"foreignKey:GameID"`
	Rating    int       `json:"rating" gorm:"not null"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type RatingResp struct {
	PlayerID string   `json:"player_id"`
	Player   UserResp `json:"player"`
	GameID   string   `json:"game_id"`
	Game     GameResp `json:"game"`
	Rating   int      `json:"rating"`
}

func (r *Rating) ToResp() *RatingResp {
	return &RatingResp{
		PlayerID: r.PlayerID,
		Player:   *r.Player.ToResp(),
		GameID:   r.GameID,
		Game:     *r.Game.ToResp(),
		Rating:   r.Rating,
	}
}

func GetRating(playerID, gameID string) (*Rating, error) {
	var rating Rating
	result := server.S.DB.First(&rating, "player_id = ? AND game_id = ?", playerID, gameID)
	if result.Error == nil {
		return &rating, nil
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		game, err := GetGame(gameID)
		if err != nil {
			return nil, err
		}

		rating = Rating{
			PlayerID: playerID,
			GameID:   gameID,
			Rating:   game.DefaultRating,
		}
		if err := server.S.DB.Create(&rating).Error; err != nil {
			return nil, err
		}
		return &rating, nil
	}
	return nil, result.Error
}
