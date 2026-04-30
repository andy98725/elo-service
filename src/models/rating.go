package models

import (
	"errors"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/util"
	"gorm.io/gorm"
)

// Rating is keyed by (player, game_queue). Each queue maintains its own
// ladder — a "ranked 1v1" queue and a "casual 2v2" queue under the same
// game keep independent ratings, even for the same player. The queue's
// DefaultRating / KFactor / ELOStrategy decide initial value and update
// behavior; see ApplyClassicElo.
type Rating struct {
	PlayerID    string    `json:"player_id" gorm:"primaryKey"`
	Player      User      `json:"player" gorm:"foreignKey:PlayerID"`
	GameQueueID string    `json:"game_queue_id" gorm:"primaryKey"`
	GameQueue   GameQueue `json:"game_queue" gorm:"foreignKey:GameQueueID;constraint:OnDelete:CASCADE"`
	Rating      int       `json:"rating" gorm:"not null"`
	CreatedAt   time.Time `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt   time.Time `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

type RatingResp struct {
	PlayerID    string        `json:"player_id"`
	Player      UserResp      `json:"player"`
	GameQueueID string        `json:"game_queue_id"`
	GameQueue   GameQueueResp `json:"game_queue"`
	Rating      int           `json:"rating"`
}

func (r *Rating) ToResp() *RatingResp {
	return &RatingResp{
		PlayerID:    r.PlayerID,
		Player:      *r.Player.ToResp(),
		GameQueueID: r.GameQueueID,
		GameQueue:   *r.GameQueue.ToResp(),
		Rating:      r.Rating,
	}
}

// GetLeaderboard returns the top-rated players for a queue, paginated.
// Ordered by rating descending, with player_id as a stable tiebreaker so
// pages don't reshuffle on equal ratings. Preloads Player so the response
// can include usernames.
func GetLeaderboard(gameQueueID string, page, pageSize int) ([]Rating, int, error) {
	var ratings []Rating
	offset := page * pageSize
	result := server.S.DB.Preload("Player").
		Where("game_queue_id = ?", gameQueueID).
		Order("rating DESC, player_id ASC").
		Offset(offset).Limit(pageSize).
		Find(&ratings)
	if result.Error != nil {
		return nil, 0, result.Error
	}
	nextPage := page + 1
	if result.RowsAffected < int64(pageSize) {
		nextPage = -1
	}
	return ratings, nextPage, nil
}

// GetRatingsForPlayers fetches current ratings for a set of players in a
// single queue. Guest IDs are filtered out (no rating row exists for them)
// and missing rows are simply absent from the returned map — the caller is
// expected to substitute the queue's DefaultRating in-memory. Does NOT
// lazy-create rows; that is reserved for paths where the rating is about
// to be mutated (e.g. ApplyClassicElo).
func GetRatingsForPlayers(gameQueueID string, playerIDs []string) (map[string]int, error) {
	out := map[string]int{}
	if len(playerIDs) == 0 {
		return out, nil
	}
	nonGuests := make([]string, 0, len(playerIDs))
	for _, p := range playerIDs {
		if !util.IsGuestID(p) {
			nonGuests = append(nonGuests, p)
		}
	}
	if len(nonGuests) == 0 {
		return out, nil
	}
	var rows []Rating
	if err := server.S.DB.
		Where("game_queue_id = ? AND player_id IN ?", gameQueueID, nonGuests).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.PlayerID] = r.Rating
	}
	return out, nil
}

// GetRating fetches the player's rating for a queue, lazy-creating at the
// queue's DefaultRating on first access.
func GetRating(playerID, gameQueueID string) (*Rating, error) {
	var rating Rating
	result := server.S.DB.First(&rating, "player_id = ? AND game_queue_id = ?", playerID, gameQueueID)
	if result.Error == nil {
		return &rating, nil
	}

	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		queue, err := GetGameQueue(gameQueueID)
		if err != nil {
			return nil, err
		}

		rating = Rating{
			PlayerID:    playerID,
			GameQueueID: gameQueueID,
			Rating:      queue.DefaultRating,
		}
		if err := server.S.DB.Create(&rating).Error; err != nil {
			return nil, err
		}
		return &rating, nil
	}
	return nil, result.Error
}
