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
			ID: "add_matchmaking_machine_ports",
			Migrate: func(tx *gorm.DB) error {
				return tx.AutoMigrate(&Game{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.AutoMigrate(&Game{})
			},
		},
	})
	if err := m.Migrate(); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
