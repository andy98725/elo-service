package hetzner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

type HetznerConnection struct {
	client *hcloud.Client
}

func InitHetznerConnection(token string) (*HetznerConnection, error) {
	client := hcloud.NewClient(hcloud.WithToken(token))
	return &HetznerConnection{client: client}, nil
}

type HostConnectionInfo struct {
	ProviderID string
	PublicIP   string
	AgentPort  int64
	AgentToken string
}

// CreateHost provisions a new Hetzner VM that runs the game-server-host-agent.
// Blocks until the agent is reachable (VM fully booted and agent running).
func (h *HetznerConnection) CreateHost(ctx context.Context, serverType string, agentPort int64) (*HostConnectionInfo, error) {
	agentToken, err := GenerateToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate agent token: %w", err)
	}

	userData := hostCloudConfig(agentPort, agentToken)
	serverName := fmt.Sprintf("game-host-%d", time.Now().Unix())

	slog.Info("Creating host VM", "serverName", serverName, "serverType", serverType)
	createOpts := hcloud.ServerCreateOpts{
		Name:      serverName,
		ServerType: &hcloud.ServerType{Name: serverType, Architecture: hcloud.ArchitectureX86, CPUType: hcloud.CPUTypeShared},
		Image:     &hcloud.Image{Name: "ubuntu-24.04"},
		Location:  &hcloud.Location{Name: "nbg1"},
		UserData:  userData,
		PublicNet: &hcloud.ServerCreatePublicNet{EnableIPv4: true, EnableIPv6: false},
		Labels:    map[string]string{"role": "game-host"},
	}

	result, _, err := h.client.Server.Create(ctx, createOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to create host VM: %w", err)
	}

	slog.Info("Host VM creating, waiting for IP", "serverName", serverName)
	var publicIP string
	for {
		srv, _, err := h.client.Server.GetByID(ctx, result.Server.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to poll host VM: %w", err)
		}
		if srv.PublicNet.IPv4.IP != nil {
			publicIP = srv.PublicNet.IPv4.IP.String()
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	slog.Info("Host VM has IP, waiting for agent", "ip", publicIP, "agentPort", agentPort)
	if err := waitForAgent(ctx, publicIP, agentPort); err != nil {
		return nil, fmt.Errorf("agent did not become ready: %w", err)
	}

	slog.Info("Host VM ready", "serverName", serverName, "ip", publicIP)
	return &HostConnectionInfo{
		ProviderID: strconv.FormatInt(result.Server.ID, 10),
		PublicIP:   publicIP,
		AgentPort:  agentPort,
		AgentToken: agentToken,
	}, nil
}

// DeleteHost shuts down and permanently deletes a host VM.
func (h *HetznerConnection) DeleteHost(ctx context.Context, providerID string) error {
	id, err := strconv.ParseInt(providerID, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid providerID %q: %w", providerID, err)
	}

	server := &hcloud.Server{ID: id}

	shutdown, _, err := h.client.Server.Shutdown(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to shutdown host VM: %w", err)
	}
	h.client.Action.WaitFor(ctx, shutdown)

	del, _, err := h.client.Server.DeleteWithResult(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to delete host VM: %w", err)
	}
	if del.Action.Status != "success" {
		return fmt.Errorf("delete action did not succeed: %s", del.Action.Status)
	}

	slog.Info("Host VM deleted", "providerID", providerID)
	return nil
}

// GenerateToken produces a random 32-byte hex token suitable for agent and game-server auth.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)
	token = strings.ReplaceAll(token, " ", "-")
	token = strings.ReplaceAll(token, "/", "-")
	return token, nil
}

// waitForAgent polls GET http://{ip}:{port}/health until it receives 200 or the context is cancelled.
func waitForAgent(ctx context.Context, ip string, port int64) error {
	url := fmt.Sprintf("http://%s:%d/health", ip, port)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	deadline := time.Now().Add(5 * time.Minute)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out waiting for agent at %s", url)
			}
			resp, err := http.Get(url)
			if err == nil {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					return nil
				}
			}
		}
	}
}

// hostCloudConfig returns the cloud-init user-data string for a host VM.
// It installs Docker and starts the game-server-host-agent container.
const hostCloudConfigTemplate = `#cloud-config
package_update: true
packages:
  - docker.io

runcmd:
  - systemctl start docker
  - docker pull docker.io/andy98725/game-server-host-agent:latest
  - docker run -d --name game-server-agent
      --restart always
      -p %d:8080
      -v /var/run/docker.sock:/var/run/docker.sock
      -e AGENT_TOKEN=%s
      docker.io/andy98725/game-server-host-agent:latest
`

func hostCloudConfig(agentPort int64, agentToken string) string {
	return fmt.Sprintf(hostCloudConfigTemplate, agentPort, agentToken)
}
