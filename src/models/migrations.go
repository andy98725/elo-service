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
	})

	if err := m.Migrate(); err != nil {
		return err
	}
	if err := server.S.DB.AutoMigrate(&User{}, &Game{}, &Match{}, &MatchResult{}, &MachineHost{}, &ServerInstance{}, &PlayerGameEntry{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
