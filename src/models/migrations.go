package models

import "github.com/andy98725/elo-service/src/server"

func Migrate() error {
	if err := server.S.DB.AutoMigrate(&User{}, &Game{}); err != nil {
		return err
	}
	server.S.Logger.Info("Database migrated")
	return nil
}
