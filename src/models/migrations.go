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
				// create the basic schema only
				return tx.Migrator().CreateTable(&User{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&User{})
			},
		},
		{
			ID: "initial",
			Migrate: func(tx *gorm.DB) error {
				return tx.AutoMigrate(&User{}, &Game{}, &Match{}, &MatchResult{})
			},
			Rollback: func(tx *gorm.DB) error {
				return tx.Migrator().DropTable(&User{}, &Game{}, &Match{}, &MatchResult{})
			},
		},
	})
	if err := m.Migrate(); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
