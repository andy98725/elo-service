package models

import (
	"math"
	"sort"

	"github.com/andy98725/elo-service/src/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ApplyClassicElo updates ratings for the non-guest players in a finished
// match using a pairwise Elo generalization. Must run inside a transaction
// (the caller's MatchEnded tx) — it locks each rating row FOR UPDATE in
// player-ID order so concurrent matches that share players cannot deadlock.
//
// Scoring: for each ordered pair (i, j) of non-guest players,
//   - both in winners → 0.5/0.5  (tied for first)
//   - i in winners, j out → 1/0
//   - i out, j in winners → 0/1
//   - both out → 0.5/0.5  (tied for last; also covers the empty-winners draw)
//
// Per-player delta is K_eff * Σ_{j≠i} (S_ij − E_ij) where K_eff = K * 2 / N
// and N is the number of non-guest players in the match. For N=2 this
// reduces to standard Elo: K * (S − E) over the single pair.
func ApplyClassicElo(tx *gorm.DB, game *Game, playerIDs, winnerIDs []string) error {
	nonGuests := make([]string, 0, len(playerIDs))
	for _, pid := range playerIDs {
		if !util.IsGuestID(pid) {
			nonGuests = append(nonGuests, pid)
		}
	}
	if len(nonGuests) < 2 {
		return nil
	}
	sort.Strings(nonGuests)

	winnerSet := make(map[string]bool, len(winnerIDs))
	for _, w := range winnerIDs {
		winnerSet[w] = true
	}

	for _, pid := range nonGuests {
		row := &Rating{PlayerID: pid, GameID: game.ID, Rating: game.DefaultRating}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(row).Error; err != nil {
			return err
		}
	}

	ratings := make([]*Rating, 0, len(nonGuests))
	for _, pid := range nonGuests {
		var r Rating
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&r, "player_id = ? AND game_id = ?", pid, game.ID).Error; err != nil {
			return err
		}
		ratings = append(ratings, &r)
	}

	n := len(ratings)
	kEff := float64(game.KFactor) * 2.0 / float64(n)
	deltas := make([]float64, n)
	for i := 0; i < n; i++ {
		iWon := winnerSet[ratings[i].PlayerID]
		for j := 0; j < n; j++ {
			if i == j {
				continue
			}
			jWon := winnerSet[ratings[j].PlayerID]
			var s float64
			switch {
			case iWon && !jWon:
				s = 1.0
			case !iWon && jWon:
				s = 0.0
			default:
				s = 0.5
			}
			e := 1.0 / (1.0 + math.Pow(10, float64(ratings[j].Rating-ratings[i].Rating)/400.0))
			deltas[i] += kEff * (s - e)
		}
	}

	for i, r := range ratings {
		r.Rating += int(math.Round(deltas[i]))
		if err := tx.Save(r).Error; err != nil {
			return err
		}
	}
	return nil
}
