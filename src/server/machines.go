package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/hcloud"
)

type HetznerConnection struct {
	client *hcloud.Client
	// sshKey *hcloud.SSHKey
}

func InitHetznerConnection() (*HetznerConnection, error) {
	token := os.Getenv("HCLOUD_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("HCLOUD_TOKEN is not set")
	}

	client := hcloud.NewClient(hcloud.WithToken(token))

	// // Find SSH key
	// sshKeyName := os.Getenv("HCLOUD_SSH_KEY_NAME")
	// if sshKeyName == "" {
	// 	return nil, fmt.Errorf("HCLOUD_SSH_KEY_NAME is not set")
	// }

	// sshKey, _, err := client.SSHKey.GetByName(context.Background(), sshKeyName)
	// if err != nil || sshKey == nil {
	// 	return nil, fmt.Errorf("SSH key %s not found: %v", sshKeyName, err)
	// }

	return &HetznerConnection{
		client: client,
		// sshKey: sshKey,
	}, nil
}

type MachineConfig struct {
	GameName                string
	MatchmakingMachineName  string
	MatchmakingMachinePorts []int64
	PlayerIDs               []string
}

type MachineConnectionInfo struct {
	MachineID string
	AuthCode  string
	PublicIP  string
}

var sanitizeRegex = regexp.MustCompile(`[^a-zA-Z0-9]`)

// Blocks until machine is created
// TODO: Prebake snapshots for each game server image
// https://chatgpt.com/share/686f845f-14a4-800a-8c0e-c775a140e265
func (h *HetznerConnection) CreateServer(ctx context.Context, config *MachineConfig) (*MachineConnectionInfo, error) {
	token, err := generateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate secure token: %w", err)
	}
	userData := hetznerUserData(config.MatchmakingMachineName, config.MatchmakingMachinePorts, token, config.PlayerIDs)

	sanitizedGameName := sanitizeRegex.ReplaceAllString(config.GameName, "")

	// Create server
	serverName := fmt.Sprintf("game-server-%s-%d", sanitizedGameName, time.Now().Unix())
	slog.Info("Creating server", "serverName", serverName)
	createOpts := hcloud.ServerCreateOpts{
		Name:       serverName,
		ServerType: &hcloud.ServerType{Name: "cx22", Architecture: hcloud.ArchitectureX86, CPUType: hcloud.CPUTypeShared},
		Labels:     map[string]string{"game": sanitizedGameName},
		Image:      &hcloud.Image{Name: "ubuntu-24.04"},
		// SSHKeys:    []*hcloud.SSHKey{h.sshKey},
		Location:  &hcloud.Location{Name: "nbg1"},
		UserData:  userData,
		PublicNet: &hcloud.ServerCreatePublicNet{EnableIPv4: true, EnableIPv6: true},
	}

	server, _, err := h.client.Server.Create(ctx, createOpts)
	if err != nil {
		slog.Error("failed to create server", "error", err)
		return nil, fmt.Errorf("failed to create server: %w", err)
	}

	slog.Info("Server is creating... waiting for IP assignment")
	var publicIP string
	for {
		srv, _, err := h.client.Server.GetByID(ctx, server.Server.ID)
		if err != nil {
			slog.Error("failed to get server", "error", err)
			return nil, fmt.Errorf("failed to get server: %w", err)
		}
		if srv.PublicNet.IPv4.IP != nil {
			// machineID = strconv.Itoa(srv.ID)
			publicIP = srv.PublicNet.IPv4.IP.String()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	return &MachineConnectionInfo{
		MachineID: serverName,
		AuthCode:  token,
		PublicIP:  publicIP,
	}, nil
}

func (h *HetznerConnection) DeleteServer(ctx context.Context, machineName string) error {
	res, _, err := h.client.Server.DeleteWithResult(ctx, &hcloud.Server{Name: machineName})
	if err != nil {
		return fmt.Errorf("failed to delete server: %w", err)
	}
	if res.Action.Status != "success" {
		return fmt.Errorf("failed to delete server: %s", res.Action.Status)
	}
	return nil
}

// #cloud-config
// package_update: true
// packages:
//   - docker.io
// runcmd:
//   - systemctl start docker
//   - docker run -d -p 7777:7777 -p 7777:7777/udp docker.io/andy98725/autominions-server:latest -token abc123 player1 player2

// #cloud-config
// package_update: true
// packages:
//   - docker.io
// runcmd:
//   - systemctl start docker
//   - docker run -d -p 8080:8080 -p 8080:8080/udp docker.io/andy98725/example-server:latest -token abc123 player1 player2

func hetznerUserData(image string, ports []int64, token string, playerIDs []string) string {
	portsStr := ""
	for _, port := range ports {
		portsStr += fmt.Sprintf("-p %d:%d -p %d:%d/udp ", port, port, port, port)
	}

	playerIDsStr := strings.Join(playerIDs, " ")

	return fmt.Sprintf(`#cloud-config
package_update: true
packages:
  - docker.io
runcmd:
  - systemctl start docker
  - docker run -d %s %s -token %s %s`, portsStr, image, token, playerIDsStr)
}
func generateToken() (string, error) {
	tokenBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, tokenBytes); err != nil {
		return "", fmt.Errorf("failed to generate secure token: %w", err)
	}
	authCode := strings.ReplaceAll(hex.EncodeToString(tokenBytes), " ", "-")
	authCode = strings.ReplaceAll(authCode, "/", "-")
	return authCode, nil
}
