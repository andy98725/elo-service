package models

import (
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

func Migrate() error {
	m := gormigrate.New(server.S.DB, gormigrate.DefaultOptions, []*gormigrate.Migration{
		{
			ID: "initial",
			Migrate: func(tx *gorm.DB) error {
				tx.Exec(`SELECT pg_advisory_lock(42)`)
				err := tx.AutoMigrate(&User{}, &Game{}, &Match{}, &MatchResult{})
				tx.Exec(`SELECT pg_advisory_unlock(42)`)
				return err
			},
			Rollback: func(tx *gorm.DB) error {
				tx.Exec(`SELECT pg_advisory_lock(42)`)
				err := tx.Migrator().DropTable(&User{}, &Game{}, &Match{}, &MatchResult{})
				tx.Exec(`SELECT pg_advisory_unlock(42)`)
				return err
			},
		},
		{
			ID: "drop_match_machine_name",
			Migrate: func(tx *gorm.DB) error {
				tx.Migrator().DropColumn(&Match{}, "machine_name")
				return nil
			},
		},
		{
			ID: "host_pool",
			Migrate: func(tx *gorm.DB) error {
				tx.Exec(`SELECT pg_advisory_lock(42)`)
				defer tx.Exec(`SELECT pg_advisory_unlock(42)`)

				if err := tx.AutoMigrate(&MachineHost{}, &ServerInstance{}); err != nil {
					return err
				}

				// Drop the old per-match machine columns from matches.
				for _, col := range []string{"machine_id", "machine_ip", "machine_logs_port"} {
					if tx.Migrator().HasColumn(&Match{}, col) {
						if err := tx.Migrator().DropColumn(&Match{}, col); err != nil {
							return err
						}
					}
				}

				return nil
			},
		},
		{
			// game_queues_split moves the per-pool matchmaking knobs out
			// of `games` into a new `game_queues` table, with a 1:N
			// relationship. Each existing game gets one auto-created
			// "primary" queue copying its current values; ratings are
			// re-keyed onto that queue's ID; matches gain a
			// game_queue_id FK pointing at it. Then the legacy queue
			// columns are dropped from `games`.
			//
			// This is the only step that touches existing rating data:
			// each row migrates 1:1 from (player, game) to (player,
			// primary_queue), so no rows are lost.
			ID: "game_queues_split",
			Migrate: func(tx *gorm.DB) error {
				tx.Exec(`SELECT pg_advisory_lock(42)`)
				defer tx.Exec(`SELECT pg_advisory_unlock(42)`)

				if err := tx.AutoMigrate(&GameQueue{}); err != nil {
					return err
				}

				// 1. Create a "primary" queue for every existing game,
				// copying the current matchmaking columns. Use INSERT ...
				// SELECT so we don't have to round-trip through Go.
				if err := tx.Exec(`
					INSERT INTO game_queues
						(id, game_id, name, created_at,
						 lobby_enabled, lobby_size,
						 matchmaking_strategy, matchmaking_machine_name, matchmaking_machine_ports,
						 elo_strategy, default_rating, k_factor, metadata_enabled)
					SELECT
						gen_random_uuid(), g.id, 'primary', g.created_at,
						g.lobby_enabled, g.lobby_size,
						g.matchmaking_strategy, g.matchmaking_machine_name, g.matchmaking_machine_ports,
						g.elo_strategy, g.default_rating, g.k_factor, g.metadata_enabled
					FROM games g
					WHERE NOT EXISTS (
						SELECT 1 FROM game_queues gq WHERE gq.game_id = g.id
					)
				`).Error; err != nil {
					return err
				}

				// 2. Add matches.game_queue_id, populate from the primary
				// queue of each match's game, then enforce NOT NULL.
				if !tx.Migrator().HasColumn(&Match{}, "game_queue_id") {
					if err := tx.Exec(`ALTER TABLE matches ADD COLUMN game_queue_id uuid`).Error; err != nil {
						return err
					}
				}
				if err := tx.Exec(`
					UPDATE matches m
					SET game_queue_id = (
						SELECT gq.id FROM game_queues gq
						WHERE gq.game_id = m.game_id
						ORDER BY gq.created_at ASC, gq.id ASC
						LIMIT 1
					)
					WHERE m.game_queue_id IS NULL
				`).Error; err != nil {
					return err
				}
				if err := tx.Exec(`ALTER TABLE matches ALTER COLUMN game_queue_id SET NOT NULL`).Error; err != nil {
					return err
				}

				// 3. Re-key ratings, OR create the table fresh.
				//
				// The pre-refactor AutoMigrate list omitted &Rating{}, so on
				// some deployments the `ratings` table never existed at all
				// (the Rating model was reachable from Go code but no GORM
				// pass ever created its table). If we're running against
				// such a DB there's nothing to rekey — let AutoMigrate
				// create the table fresh in the new (player_id,
				// game_queue_id) shape.
				if !tx.Migrator().HasTable(&Rating{}) {
					if err := tx.AutoMigrate(&Rating{}); err != nil {
						return err
					}
				} else {
					// Existing data: add game_queue_id, populate from the
					// primary queue, swap PK from (player, game) to
					// (player, game_queue), drop game_id.
					if !tx.Migrator().HasColumn(&Rating{}, "game_queue_id") {
						if err := tx.Exec(`ALTER TABLE ratings ADD COLUMN game_queue_id uuid`).Error; err != nil {
							return err
						}
					}
					if err := tx.Exec(`
						UPDATE ratings r
						SET game_queue_id = (
							SELECT gq.id FROM game_queues gq
							WHERE gq.game_id = r.game_id
							ORDER BY gq.created_at ASC, gq.id ASC
							LIMIT 1
						)
						WHERE r.game_queue_id IS NULL
					`).Error; err != nil {
						return err
					}
					if err := tx.Exec(`ALTER TABLE ratings ALTER COLUMN game_queue_id SET NOT NULL`).Error; err != nil {
						return err
					}
					// Drop the old PK and add the new one. Postgres names the
					// PK constraint after the table by default.
					if err := tx.Exec(`ALTER TABLE ratings DROP CONSTRAINT IF EXISTS ratings_pkey`).Error; err != nil {
						return err
					}
					if err := tx.Exec(`ALTER TABLE ratings ADD PRIMARY KEY (player_id, game_queue_id)`).Error; err != nil {
						return err
					}
					if tx.Migrator().HasColumn(&Rating{}, "game_id") {
						if err := tx.Migrator().DropColumn(&Rating{}, "game_id"); err != nil {
							return err
						}
					}
				}

				// 4. Drop the legacy per-pool columns from games.
				legacyGameCols := []string{
					"lobby_enabled", "lobby_size",
					"matchmaking_strategy", "matchmaking_machine_name", "matchmaking_machine_ports",
					"elo_strategy", "default_rating", "k_factor", "metadata_enabled",
				}
				for _, col := range legacyGameCols {
					if tx.Migrator().HasColumn(&Game{}, col) {
						if err := tx.Migrator().DropColumn(&Game{}, col); err != nil {
							return err
						}
					}
				}

				return nil
			},
		},
	})

	if err := m.Migrate(); err != nil {
		return err
	}
	if err := server.S.DB.AutoMigrate(&User{}, &Game{}, &GameQueue{}, &Match{}, &MatchResult{}, &MachineHost{}, &ServerInstance{}, &Rating{}, &PlayerGameEntry{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
