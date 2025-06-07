package models

import (
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
)

func Migrate() error {
	if err := server.S.DB.AutoMigrate(&User{}, &Game{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
