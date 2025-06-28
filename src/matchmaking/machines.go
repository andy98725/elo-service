package matchmaking

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const portTemplate = `{
	"port": %d,
	"handlers": [ "tls", "http" ]
}`

const jsonTemplate = `{
	"config": {
		"init": {
			"cmd": [ "-token", "%s", %s]
		},
		"image": "%s",
		"services": [ {
			"ports": [ %s ]
		} ],
		"auto_destroy": true,
		"restart": {
			"policy": "no"
		},
		"network": "game-servers",
		"guest": {
			"cpu_kind": "shared",
			"cpus": 1,
			"memory_mb": 256
		}
	}
}`

func SpawnMachine(gameID string, playerIDs []string) (*models.MachineConnectionInfo, error) {
	game, err := models.GetGame(gameID)
	if err != nil {
		return nil, err
	}

	// Generate 32 random bytes and encode as hex for secure token
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate secure token: %w", err)
	}
	authCode := strings.ReplaceAll(hex.EncodeToString(tokenBytes), " ", "-")
	authCode = strings.ReplaceAll(authCode, "/", "-")

	playersStr := "\"" + strings.Join(playerIDs, "\", \"") + "\""
	machineName := game.MatchmakingMachineName
	if machineName == "" {
		machineName = "docker.io/andy98725/example-server:latest"
	}
	ports := ""
	for i, port := range game.MatchmakingMachinePorts {
		if i > 0 {
			ports += ","
		}
		ports += fmt.Sprintf(portTemplate, port)
	}
	jsonData := fmt.Sprintf(jsonTemplate, authCode, playersStr, machineName, ports)

	// Create HTTP request
	url := fmt.Sprintf("%s/v1/apps/%s/machines", server.S.Config.FlyAPIHostname, server.S.Config.FlyAppName)
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+server.S.Config.FlyAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("fly.io API returned status %d: %s", resp.StatusCode, string(body))
	}

	slog.Info("Spawned machine", "body", string(body))

	return &models.MachineConnectionInfo{
		ConnectionAddress: machineName, //TODO: Get connection address from fly.io API
		AuthCode:          authCode,
	}, nil
}
