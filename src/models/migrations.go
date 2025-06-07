package models

import (
	"log/slog"

	"github.com/andy98725/elo-service/src/server"
)

func Migrate() error {

	// Add matchmakingMachineName to existing games
	if err := server.S.DB.Exec("ALTER TABLE games ADD COLUMN IF NOT EXISTS matchmaking_machine_name VARCHAR(255)").Error; err != nil {
		return err
	}

	// TEMP: Update any existing records that might have null values
	if err := server.S.DB.Exec("UPDATE games SET matchmaking_machine_name = 'battle-bots-server' WHERE matchmaking_machine_name IS NULL").Error; err != nil {
		return err
	}

	if err := server.S.DB.AutoMigrate(&User{}, &Game{}); err != nil {
		return err
	}

	slog.Info("Database migrated")
	return nil
}
