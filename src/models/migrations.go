package models

import (
	"context"
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func Migrate() error {
	server.S.DB = server.S.DB.Debug()
	server.S.DB.Logger.LogMode(logger.Info)
	server.S.DB.Logger.Info(context.Background(), "Migrating database")

	m := gormigrate.New(server.S.DB, gormigrate.DefaultOptions, []*gormigrate.Migration{
		{
			ID: "initial_create_users",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasTable(&User{}) {
					return tx.Migrator().DropTable(&User{})
				}
				return tx.Migrator().CreateTable(&User{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&User{})
			},
		},
		{
			ID: "initial_create_games",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasTable(&Game{}) {
					return tx.Migrator().DropTable(&Game{})
				}
				return tx.Migrator().CreateTable(&Game{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&Game{})
			},
		},
		{
			ID: "initial_create_matches",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasTable(&Match{}) {
					return tx.Migrator().DropTable(&Match{})
				}
				return tx.Migrator().CreateTable(&Match{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&Match{})
			},
		},
		{
			ID: "initial_create_match_results",
			Migrate: func(tx *gorm.DB) error {
				if tx.Migrator().HasTable(&MatchResult{}) {
					return tx.Migrator().DropTable(&MatchResult{})
				}
				return tx.Migrator().CreateTable(&MatchResult{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&MatchResult{})
			},
		},
	})
	if err := m.Migrate(); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
