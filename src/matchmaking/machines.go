package matchmaking

import (
	"github.com/andy98725/elo-service/src/models"
	"github.com/google/uuid"
)

func SpawnMachine(gameID string, playerIDs []string) (*models.MachineConnectionInfo, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return nil, err
	}
	authCode := uuid.NewString()
	machineName := game.MatchmakingMachineName

	//TODO: Spawn machine

	return &models.MachineConnectionInfo{
		MachineName: machineName,
		AuthCode:    authCode,
	}, nil
}
