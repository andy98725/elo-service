package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const serviceTemplate = `{
            "ports": [{
                "port": %d,
                "handlers": ["tls","http"]
              }],
            "protocol": "tcp",
            "internal_port": %d
          }`

const jsonTemplate = `{
	"name": "%s",
	"config": {
		"init": {
			"cmd": [ "-token", "%s", %s]
		},
		"image": "%s",
		"services": [%s],
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

type MachineConnectionInfo struct {
	PublicPorts []int64
	AuthCode    string
	MachineID   string
}

// FlyMachineResponse represents the response from Fly.io API when creating a machine
type FlyMachineResponse struct {
	ID string `json:"id"`
}

type MachineConfig struct {
	GameName                string
	MatchmakingMachineName  string
	MatchmakingMachinePorts []int64
	PlayerIDs               []string
}

func StartMachine(config *MachineConfig) (*MachineConnectionInfo, error) {

	// Generate 32 random bytes and encode as hex for secure token
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return nil, fmt.Errorf("failed to generate secure token: %w", err)
	}
	authCode := strings.ReplaceAll(hex.EncodeToString(tokenBytes), " ", "-")
	authCode = strings.ReplaceAll(authCode, "/", "-")

	machineName := fmt.Sprintf("%s-%d", config.GameName, time.Now().Unix())

	playersStr := "\"" + strings.Join(config.PlayerIDs, "\", \"") + "\""
	imageName := config.MatchmakingMachineName
	if imageName == "" {
		imageName = "docker.io/andy98725/example-server:latest"
	}

	publicPorts, err := S.Redis.AllocatePorts(context.Background(), machineName, len(config.MatchmakingMachinePorts))
	if err != nil {
		return nil, fmt.Errorf("failed to allocate ports: %w", err)
	}
	svcs := ""
	for i, port := range config.MatchmakingMachinePorts {
		if i > 0 {
			svcs += ","
		}
		svcs += fmt.Sprintf(serviceTemplate, publicPorts[i], port)
	}
	jsonData := fmt.Sprintf(jsonTemplate, machineName, authCode, playersStr, imageName, svcs)

	// Create HTTP request
	url := fmt.Sprintf("%s/v1/apps/%s/machines", S.Config.FlyAPIHostname, S.Config.FlyAppName)
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+S.Config.FlyAPIKey)
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

	// Parse the response to get the machine ID
	var machineResp FlyMachineResponse
	if err := json.Unmarshal(body, &machineResp); err != nil {
		return nil, fmt.Errorf("failed to parse Fly.io API response: %w", err)
	}

	if machineResp.ID == "" {
		return nil, fmt.Errorf("no machine ID in Fly.io API response")
	}

	return &MachineConnectionInfo{
		PublicPorts: publicPorts,
		AuthCode:    authCode,
		MachineID:   machineResp.ID,
	}, nil
}

func StopMachine(machineID string) error {
	slog.Info("stopping machine", "machineID", machineID)
	url := fmt.Sprintf("%s/v1/apps/%s/machines/%s", S.Config.FlyAPIHostname, S.Config.FlyAppName, machineID)
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+S.Config.FlyAPIKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to make HTTP request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("fly.io API returned status %d: %s", resp.StatusCode, string(body))
	}

	err = S.Redis.FreePorts(context.Background(), machineID)
	if err != nil {
		return fmt.Errorf("failed to free ports: %w", err)
	}

	return nil
}
