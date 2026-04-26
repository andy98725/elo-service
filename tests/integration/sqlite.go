package integration

// The CREATE TABLE statements below are hand-rolled because the production
// migrations use Postgres-specific features (advisory locks, pq.StringArray
// with `text[]`, gen_random_uuid()) that SQLite does not support. Whenever
// you add or rename a column on a model in src/models, mirror the change
// here. assertSchemaMatchesModels (run from migrateForSQLite) compares the
// columns GORM derives from each model against this schema and fails fast
// with a clear error if they drift.

import (
	"fmt"
	"sync"

	"github.com/andy98725/elo-service/src/models"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
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

	return assertSchemaMatchesModels(db)
}

// assertSchemaMatchesModels parses each GORM model and confirms every column
// it expects exists in the SQLite test schema. Catches the "added a field on
// a model and forgot to update sqlite.go" failure mode early with a clear
// error instead of an opaque "no such column" deep inside a test.
func assertSchemaMatchesModels(db *gorm.DB) error {
	cache := &sync.Map{}
	ns := schema.NamingStrategy{}
	checks := []interface{}{
		&models.User{}, &models.Game{}, &models.Match{},
		&models.MatchResult{}, &models.Rating{},
	}
	for _, m := range checks {
		s, err := schema.Parse(m, cache, ns)
		if err != nil {
			return fmt.Errorf("schema.Parse %T: %w", m, err)
		}
		for _, col := range s.DBNames {
			if !db.Migrator().HasColumn(m, col) {
				return fmt.Errorf(
					"schema drift: model %T declares column %q but it is missing from tests/integration/sqlite.go — add it to the CREATE TABLE",
					m, col,
				)
			}
		}
	}
	return nil
}
