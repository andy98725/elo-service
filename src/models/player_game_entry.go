package models

import (
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"gorm.io/gorm"
)

// PlayerGameEntry is a per-(game, player) JSON key-value entry. The
// (server_authored) column is part of the PK so the same key can exist
// independently in the player-authored and server-authored namespaces —
// see the writeup in CLAUDE.md.
//
// Player-authored entries are written by the owning player via JWT auth;
// server-authored entries are written by the game server using the active
// match's auth code. Each side can READ both halves but only WRITE its own.
type PlayerGameEntry struct {
	GameID         string          `json:"game_id" gorm:"primaryKey;type:uuid"`
	Game           Game            `json:"-" gorm:"foreignKey:GameID;constraint:OnDelete:CASCADE"`
	PlayerID       string          `json:"player_id" gorm:"primaryKey"`
	Player         User            `json:"-" gorm:"foreignKey:PlayerID;constraint:OnDelete:CASCADE"`
	Key            string          `json:"key" gorm:"primaryKey;size:128"`
	ServerAuthored bool            `json:"server_authored" gorm:"primaryKey"`
	Value          json.RawMessage `json:"value" gorm:"type:jsonb;not null"`
	CreatedAt      time.Time       `json:"created_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
	UpdatedAt      time.Time       `json:"updated_at" gorm:"not null;default:CURRENT_TIMESTAMP"`
}

const (
	// PlayerGameEntryMaxValueBytes caps a single entry's serialized JSON
	// at 64KB. Enforced in handlers (not the schema) so we can change it
	// without a migration.
	PlayerGameEntryMaxValueBytes = 64 * 1024
	// PlayerGameEntryMaxKeyLen mirrors the size constraint on the column.
	PlayerGameEntryMaxKeyLen = 128
)

var (
	ErrPlayerGameEntryKeyInvalid = errors.New("invalid key")
	ErrPlayerGameEntryValueTooLarge = errors.New("value too large")
	ErrPlayerGameEntryValueInvalid  = errors.New("value is not valid JSON")
)

var playerGameEntryKeyRe = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

// ValidatePlayerGameEntryKey returns ErrPlayerGameEntryKeyInvalid for
// anything outside [a-zA-Z0-9._-]{1,128}.
func ValidatePlayerGameEntryKey(key string) error {
	if !playerGameEntryKeyRe.MatchString(key) {
		return ErrPlayerGameEntryKeyInvalid
	}
	return nil
}

// ValidatePlayerGameEntryValue checks the value is valid JSON within the
// size cap. Empty bytes count as invalid — explicit `null` is fine.
func ValidatePlayerGameEntryValue(value json.RawMessage) error {
	if len(value) == 0 {
		return ErrPlayerGameEntryValueInvalid
	}
	if len(value) > PlayerGameEntryMaxValueBytes {
		return ErrPlayerGameEntryValueTooLarge
	}
	if !json.Valid(value) {
		return ErrPlayerGameEntryValueInvalid
	}
	return nil
}

// UpsertPlayerGameEntry inserts or replaces the entry at
// (gameID, playerID, key, serverAuthored). Caller is responsible for
// validating key and value first.
func UpsertPlayerGameEntry(gameID, playerID, key string, serverAuthored bool, value json.RawMessage) error {
	entry := PlayerGameEntry{
		GameID:         gameID,
		PlayerID:       playerID,
		Key:            key,
		ServerAuthored: serverAuthored,
		Value:          value,
	}
	// ON CONFLICT (full PK) DO UPDATE — GORM emits the right SQL for both
	// Postgres (upsert) and SQLite (REPLACE-ish).
	return server.S.DB.Save(&entry).Error
}

// GetPlayerGameEntry returns the entry at the full PK or
// gorm.ErrRecordNotFound.
func GetPlayerGameEntry(gameID, playerID, key string, serverAuthored bool) (*PlayerGameEntry, error) {
	var entry PlayerGameEntry
	err := server.S.DB.First(&entry,
		"game_id = ? AND player_id = ? AND key = ? AND server_authored = ?",
		gameID, playerID, key, serverAuthored).Error
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// ListPlayerGameEntries returns all entries for one (game, player) on the
// requested side, ordered by key for stable pagination later. No paging
// today — see open question in CLAUDE.md if this row count grows.
func ListPlayerGameEntries(gameID, playerID string, serverAuthored bool) ([]PlayerGameEntry, error) {
	var entries []PlayerGameEntry
	err := server.S.DB.
		Where("game_id = ? AND player_id = ? AND server_authored = ?", gameID, playerID, serverAuthored).
		Order("key ASC").
		Find(&entries).Error
	return entries, err
}

// DeletePlayerGameEntry removes the entry at the full PK. Returns
// gorm.ErrRecordNotFound if the row didn't exist.
func DeletePlayerGameEntry(gameID, playerID, key string, serverAuthored bool) error {
	res := server.S.DB.
		Where("game_id = ? AND player_id = ? AND key = ? AND server_authored = ?",
			gameID, playerID, key, serverAuthored).
		Delete(&PlayerGameEntry{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
