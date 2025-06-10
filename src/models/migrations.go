package models

import (
	"context"
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
	"gorm.io/gorm/logger"
)

func Migrate() error {
	server.S.DB = server.S.DB.Debug()
	server.S.DB.Logger.LogMode(logger.Info)
	server.S.DB.Logger.Info(context.Background(), "Migrating database")
	if err := server.S.DB.AutoMigrate(&User{}, &Game{}, &Match{}, &MatchResult{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
