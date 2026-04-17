package hetzner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ContainerConfig is the payload sent to the host agent to start a game server container.
type ContainerConfig struct {
	Image     string   `json:"image"`
	GamePorts []int64  `json:"game_ports"`
	HostPorts []int64  `json:"host_ports"`
	Token     string   `json:"token"`
	PlayerIDs []string `json:"player_ids"`
}

type startContainerResponse struct {
	ContainerID string `json:"container_id"`
}

func agentURL(hostIP string, agentPort int64, path string) string {
	return fmt.Sprintf("http://%s:%d%s", hostIP, agentPort, path)
}

func agentDo(ctx context.Context, method, url, token string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return http.DefaultClient.Do(req)
}

func agentError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("agent returned HTTP %d: %s", resp.StatusCode, string(body))
}

// StartContainer tells the host agent to pull and start a game server container.
// Returns the Docker container ID.
func StartContainer(ctx context.Context, hostIP string, agentPort int64, agentToken string, cfg ContainerConfig) (string, error) {
	url := agentURL(hostIP, agentPort, "/containers")
	resp, err := agentDo(ctx, http.MethodPost, url, agentToken, cfg)
	if err != nil {
		return "", fmt.Errorf("start container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", agentError(resp)
	}

	var result startContainerResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode agent response: %w", err)
	}
	return result.ContainerID, nil
}

// StopContainer tells the host agent to stop and remove a running container.
func StopContainer(ctx context.Context, hostIP string, agentPort int64, agentToken string, containerID string) error {
	url := agentURL(hostIP, agentPort, "/containers/"+containerID)
	resp, err := agentDo(ctx, http.MethodDelete, url, agentToken, nil)
	if err != nil {
		return fmt.Errorf("stop container: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return agentError(resp)
	}
	return nil
}

// GetContainerLogs fetches the full stdout+stderr log for a container from the host agent.
func GetContainerLogs(ctx context.Context, hostIP string, agentPort int64, agentToken string, containerID string) ([]byte, error) {
	url := agentURL(hostIP, agentPort, "/containers/"+containerID+"/logs")
	resp, err := agentDo(ctx, http.MethodGet, url, agentToken, nil)
	if err != nil {
		return nil, fmt.Errorf("get container logs: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, agentError(resp)
	}
	return io.ReadAll(resp.Body)
}
