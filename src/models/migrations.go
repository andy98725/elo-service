package models

import (
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
)

func Migrate() error {
	server.S.DB = server.S.DB.Debug()
	if err := server.S.DB.AutoMigrate(&User{}); err != nil {
		return err
	}
	if err := server.S.DB.AutoMigrate(&Game{}); err != nil {
		return err
	}
	if err := server.S.DB.AutoMigrate(&Match{}); err != nil {
		return err
	}
	if err := server.S.DB.AutoMigrate(&MatchResult{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
