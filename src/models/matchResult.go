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
	// Artifacts is the list of artifact names the game server uploaded
	// during this match (via POST /match/artifact). Names only — the
	// per-artifact metadata (content_type, size, uploaded_at) lives in
	// S3 at artifacts/<matchID>/index.json. Populated in EndMatch by
	// reading the S3 index, so a match that uploaded zero artifacts
	// keeps an empty array. Used by /user/artifacts to filter quickly
	// in SQL without touching S3.
	Artifacts pq.StringArray `json:"artifacts" gorm:"type:text[];default:'{}'"`
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

func MatchEnded(matchID string, winnerIDs []string, result string, logsKey string, artifacts []string, adjustRatings bool) (*MatchResult, error) {
	match, err := GetMatch(matchID)
	if err != nil {
		return nil, err
	}

	if artifacts == nil {
		artifacts = []string{}
	}
	// Preserve the Match's UUID as the MatchResult's UUID so the same
	// match_id flows through the whole lifecycle: matchmaker WS, player
	// reconnect, spectator stream, replay archive, /results, and the
	// artifact routes all key on one ID. (Match and MatchResult are
	// different tables; sharing a UUID isn't a PK conflict, and the
	// Match row is deleted in this same transaction so they never
	// coexist long.)
	matchResult := &MatchResult{
		ID:        matchID,
		GameID:    match.GameID,
		Players:   match.Players,
		GuestIDs:  match.GuestIDs,
		WinnerIDs: winnerIDs,
		Result:    result,
		LogsKey:   logsKey,
		Artifacts: pq.StringArray(artifacts),
	}
	slog.Info("Match ended", "matchID", matchID, "winnerIDs", winnerIDs, "result", result, "logsKey", logsKey, "adjustRatings", adjustRatings)

	// Report result, delete match, and (if applicable) update ratings in one
	// transaction so the rating delta cannot drift from the result that
	// justified it.
	err = server.S.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(matchResult).Error; err != nil {
			return err
		}

		// Clear the many2many join rows before deleting the match itself.
		// GORM does not auto-cascade many2many on Delete, and the FK
		// (fk_match_players_match) blocks the delete otherwise. Bug
		// surfaces only when the match contained registered users —
		// guest-only matches store IDs inline in matches.guest_ids and
		// never touch this join table.
		if err := tx.Exec("DELETE FROM match_players WHERE match_id = ?", matchID).Error; err != nil {
			return err
		}

		if err := tx.Delete(&Match{ID: matchID}).Error; err != nil {
			return err
		}

		if adjustRatings && match.Game.ELOStrategy == ELO_STRATEGY_CLASSIC {
			playerIDs := make([]string, 0, len(match.Players)+len(match.GuestIDs))
			for _, p := range match.Players {
				playerIDs = append(playerIDs, p.ID)
			}
			playerIDs = append(playerIDs, []string(match.GuestIDs)...)
			if err := ApplyClassicElo(tx, &match.Game, playerIDs, winnerIDs); err != nil {
				return err
			}
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

// GetMatchResultsWithArtifactsForPlayer returns the player's match
// results that have at least one uploaded artifact, optionally filtered
// to a single game and/or a set of artifact names.
//
// filterNames empty → return all results that have any artifact.
// filterNames non-empty → return results whose artifacts overlap with
// the supplied set (OR semantics — "has any of these names").
//
// Implementation note: the artifact-overlap filter is applied in Go
// rather than via the Postgres `&&` operator so the same code path works
// in the SQLite test harness. For users, a portable JOIN through
// match_result_players selects candidate rows; for guests we fall back
// to fetching by game_id (or all) and filtering guest_ids in Go, same
// pattern used by GetActiveMatchesInGameForPlayer for the same reason.
//
// Pagination is applied AFTER the in-memory filter so a "page 2" call
// is consistent with what page 1 returned (caveat: a concurrently
// uploaded artifact between calls can shift entries; not worth solving
// for v1).
func GetMatchResultsWithArtifactsForPlayer(playerID string, gameID *string, filterNames []string, page, pageSize int) ([]MatchResult, int, error) {
	var candidates []MatchResult

	if util.IsGuestID(playerID) {
		q := server.S.DB.Preload("Game").Preload("Players").Order("created_at DESC")
		if gameID != nil {
			q = q.Where("game_id = ?", *gameID)
		}
		if err := q.Find(&candidates).Error; err != nil {
			return nil, 0, err
		}
		// Keep only results this guest participated in.
		filtered := candidates[:0]
		for _, mr := range candidates {
			for _, gid := range mr.GuestIDs {
				if gid == playerID {
					filtered = append(filtered, mr)
					break
				}
			}
		}
		candidates = filtered
	} else {
		q := server.S.DB.
			Model(&MatchResult{}).
			Preload("Game").
			Preload("Players").
			Joins("JOIN match_result_players mrp ON mrp.match_result_id = match_results.id").
			Where("mrp.user_id = ?", playerID).
			Order("match_results.created_at DESC")
		if gameID != nil {
			q = q.Where("match_results.game_id = ?", *gameID)
		}
		if err := q.Find(&candidates).Error; err != nil {
			return nil, 0, err
		}
	}

	// Filter to results with artifacts; if filterNames is non-empty,
	// require name overlap.
	matched := make([]MatchResult, 0, len(candidates))
	wantSet := make(map[string]struct{}, len(filterNames))
	for _, n := range filterNames {
		wantSet[n] = struct{}{}
	}
	for _, mr := range candidates {
		if len(mr.Artifacts) == 0 {
			continue
		}
		if len(wantSet) == 0 {
			matched = append(matched, mr)
			continue
		}
		for _, name := range mr.Artifacts {
			if _, ok := wantSet[name]; ok {
				matched = append(matched, mr)
				break
			}
		}
	}

	// Paginate the filtered slice. Returning -1 when this page exhausts
	// the list keeps the contract identical to GetMatchResultsOfPlayer.
	offset := page * pageSize
	if offset >= len(matched) {
		return []MatchResult{}, -1, nil
	}
	end := offset + pageSize
	if end > len(matched) {
		end = len(matched)
	}
	nextPage := page + 1
	if end == len(matched) {
		nextPage = -1
	}
	return matched[offset:end], nextPage, nil
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
