package integration

import (
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func migrateForSQLite(db *gorm.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			email TEXT NOT NULL UNIQUE,
			password TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_admin INTEGER DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS games (
			id TEXT PRIMARY KEY,
			owner_id TEXT NOT NULL,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			guests_allowed INTEGER DEFAULT 1,
			lobby_size INTEGER DEFAULT 2,
			matchmaking_strategy TEXT NOT NULL DEFAULT 'random',
			matchmaking_machine_name TEXT NOT NULL,
			matchmaking_machine_ports TEXT DEFAULT '{}',
			elo_strategy TEXT NOT NULL DEFAULT 'unranked',
			default_rating INTEGER DEFAULT 1000,
			public_results INTEGER DEFAULT 1,
			public_match_logs INTEGER DEFAULT 1,
			FOREIGN KEY (owner_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS matches (
			id TEXT PRIMARY KEY,
			game_id TEXT NOT NULL,
			machine_name TEXT,
			machine_id INTEGER,
			machine_ip TEXT,
			machine_logs_port INTEGER,
			guest_ids TEXT DEFAULT '{}',
			auth_code TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (game_id) REFERENCES games(id)
		)`,
		`CREATE TABLE IF NOT EXISTS match_players (
			match_id TEXT,
			user_id TEXT,
			PRIMARY KEY (match_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS match_results (
			id TEXT PRIMARY KEY,
			game_id TEXT NOT NULL,
			guest_ids TEXT DEFAULT '{}',
			winner_ids TEXT DEFAULT '{}',
			result TEXT NOT NULL,
			logs_key TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (game_id) REFERENCES games(id)
		)`,
		`CREATE TABLE IF NOT EXISTS match_result_players (
			match_result_id TEXT,
			user_id TEXT,
			PRIMARY KEY (match_result_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS ratings (
			player_id TEXT,
			game_id TEXT,
			rating INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (player_id, game_id)
		)`,
	}
	for _, stmt := range stmts {
		if err := db.Exec(stmt).Error; err != nil {
			return err
		}
	}

	db.Callback().Create().Before("gorm:create").Register("uuid_generate", func(tx *gorm.DB) {
		if tx.Statement.Schema == nil {
			return
		}
		for _, field := range tx.Statement.Schema.PrimaryFields {
			if field.DBName != "id" {
				continue
			}
			val, isZero := field.ValueOf(tx.Statement.Context, tx.Statement.ReflectValue)
			if isZero || val == nil || val == "" {
				field.Set(tx.Statement.Context, tx.Statement.ReflectValue, uuid.New().String())
			}
		}
	})

	return nil
}
