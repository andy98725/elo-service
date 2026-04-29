package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

var dockerClient *client.Client
var agentToken string

// internalPortShift is added to every public host port when binding the
// game container's docker port. On wildcard-TLS hosts, Caddy listens on
// the public ports and reverse-proxies to localhost:port+shift, which is
// where the container is actually bound. Set via INTERNAL_PORT_SHIFT env.
// Defaults to 0 (no shift, legacy direct bind) so older host VMs keep
// working without changes.
var internalPortShift int64

type startContainerRequest struct {
	Image     string   `json:"image"`
	GamePorts []int64  `json:"game_ports"`
	HostPorts []int64  `json:"host_ports"`
	Token     string   `json:"token"`
	PlayerIDs []string `json:"player_ids"`
}

type startContainerResponse struct {
	ContainerID string `json:"container_id"`
}

func main() {
	agentToken = os.Getenv("AGENT_TOKEN")
	if agentToken == "" {
		log.Fatal("AGENT_TOKEN is not set")
	}

	if v := os.Getenv("INTERNAL_PORT_SHIFT"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			log.Fatalf("INTERNAL_PORT_SHIFT must be an integer, got %q: %v", v, err)
		}
		internalPortShift = n
		log.Printf("INTERNAL_PORT_SHIFT=%d (TLS-enabled host)", internalPortShift)
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	var err error
	dockerClient, err = client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create Docker client: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handleHealth)
	mux.HandleFunc("POST /containers", handleStartContainer)
	mux.HandleFunc("DELETE /containers/{id}", handleStopContainer)
	mux.HandleFunc("GET /containers/{id}/health", handleContainerHealth)
	mux.HandleFunc("GET /containers/{id}/logs", handleContainerLogs)
	mux.HandleFunc("GET /containers/stats", handleContainerStats)

	log.Printf("Agent listening on :%s", port)
	if err := http.ListenAndServe(":"+port, authMiddleware(mux)); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// /health and container health checks are unauthenticated so the
		// elo-service can poll them without storing the agent token client-side.
		if r.URL.Path == "/health" || strings.HasSuffix(r.URL.Path, "/health") {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != agentToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func handleStartContainer(w http.ResponseWriter, r *http.Request) {
	var req startContainerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.GamePorts) != len(req.HostPorts) {
		http.Error(w, "game_ports and host_ports must have equal length", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	reader, err := dockerClient.ImagePull(ctx, req.Image, image.PullOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to pull image: %v", err), http.StatusInternalServerError)
		return
	}
	io.Copy(io.Discard, reader)
	reader.Close()

	portBindings := nat.PortMap{}
	exposedPorts := nat.PortSet{}
	for i, gamePort := range req.GamePorts {
		// On TLS-enabled hosts (internalPortShift != 0), Caddy listens on
		// the public TCP port and reverse-proxies to localhost:port+shift,
		// where the container is actually bound. UDP can't be TLS-wrapped
		// (Caddy doesn't speak it, browsers can't either) so UDP stays
		// bound directly on the public port for native clients.
		publicPort := req.HostPorts[i]
		tcpBindPort := publicPort + internalPortShift
		// TCP: shifted (so Caddy can listen on publicPort)
		tcpKey := nat.Port(fmt.Sprintf("%d/tcp", gamePort))
		exposedPorts[tcpKey] = struct{}{}
		portBindings[tcpKey] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", tcpBindPort)}}
		// UDP: direct
		udpKey := nat.Port(fmt.Sprintf("%d/udp", gamePort))
		exposedPorts[udpKey] = struct{}{}
		portBindings[udpKey] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", publicPort)}}
	}

	cmd := []string{"-token", req.Token}
	cmd = append(cmd, req.PlayerIDs...)

	// TODO: add Resources (Memory, NanoCPUs) to HostConfig once /containers/stats
	// gives us per-game footprint numbers from a real load test. Today every
	// container on a host shares the VM's CPU/RAM unbounded — a noisy game can
	// starve its neighbors. Right-size from measurements before raising slot
	// counts.
	created, err := dockerClient.ContainerCreate(ctx,
		&container.Config{Image: req.Image, Cmd: cmd, ExposedPorts: exposedPorts},
		&container.HostConfig{PortBindings: portBindings},
		nil, nil, "")
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to create container: %v", err), http.StatusInternalServerError)
		return
	}

	if err := dockerClient.ContainerStart(ctx, created.ID, container.StartOptions{}); err != nil {
		dockerClient.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
		http.Error(w, fmt.Sprintf("failed to start container: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(startContainerResponse{ContainerID: created.ID})
}

func handleStopContainer(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")
	ctx := r.Context()

	timeout := 10
	if err := dockerClient.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout}); err != nil {
		http.Error(w, fmt.Sprintf("failed to stop container: %v", err), http.StatusInternalServerError)
		return
	}
	if err := dockerClient.ContainerRemove(ctx, containerID, container.RemoveOptions{}); err != nil {
		http.Error(w, fmt.Sprintf("failed to remove container: %v", err), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func handleContainerHealth(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")
	info, err := dockerClient.ContainerInspect(r.Context(), containerID)
	if err != nil {
		http.Error(w, "container not found", http.StatusNotFound)
		return
	}
	if info.State.Running {
		w.WriteHeader(http.StatusOK)
	} else {
		http.Error(w, "container not running", http.StatusServiceUnavailable)
	}
}

type containerStat struct {
	ContainerID      string  `json:"container_id"`
	Name             string  `json:"name,omitempty"`
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryUsedBytes  uint64  `json:"memory_used_bytes"`
	MemoryLimitBytes uint64  `json:"memory_limit_bytes"`
	NetworkRxBytes   uint64  `json:"network_rx_bytes"`
	NetworkTxBytes   uint64  `json:"network_tx_bytes"`
}

func handleContainerStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	containers, err := dockerClient.ContainerList(ctx, container.ListOptions{})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to list containers: %v", err), http.StatusInternalServerError)
		return
	}
	out := make([]containerStat, 0, len(containers))
	for _, c := range containers {
		s, err := readContainerStats(ctx, c.ID)
		if err != nil {
			log.Printf("stats: skipping container %s: %v", c.ID, err)
			continue
		}
		if len(c.Names) > 0 {
			s.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		out = append(out, *s)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func readContainerStats(ctx context.Context, id string) (*containerStat, error) {
	// stream=false makes the daemon read two samples internally and populate
	// precpu_stats, which we need for the CPU% delta.
	resp, err := dockerClient.ContainerStats(ctx, id, false)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var v container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}

	cpuPercent := 0.0
	cpuDelta := float64(v.CPUStats.CPUUsage.TotalUsage) - float64(v.PreCPUStats.CPUUsage.TotalUsage)
	systemDelta := float64(v.CPUStats.SystemUsage) - float64(v.PreCPUStats.SystemUsage)
	if systemDelta > 0 && cpuDelta > 0 {
		numCPUs := float64(v.CPUStats.OnlineCPUs)
		if numCPUs == 0 {
			numCPUs = float64(len(v.CPUStats.CPUUsage.PercpuUsage))
		}
		if numCPUs == 0 {
			numCPUs = 1
		}
		cpuPercent = (cpuDelta / systemDelta) * numCPUs * 100.0
	}

	// Subtract page cache so the number reflects actual working set, matching
	// what `docker stats` displays. cgroup v2 uses inactive_file; v1 uses cache.
	memUsed := v.MemoryStats.Usage
	if c, ok := v.MemoryStats.Stats["total_inactive_file"]; ok && c < memUsed {
		memUsed -= c
	} else if c, ok := v.MemoryStats.Stats["cache"]; ok && c < memUsed {
		memUsed -= c
	}

	var rx, tx uint64
	for _, n := range v.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}

	return &containerStat{
		ContainerID:      id,
		CPUPercent:       cpuPercent,
		MemoryUsedBytes:  memUsed,
		MemoryLimitBytes: v.MemoryStats.Limit,
		NetworkRxBytes:   rx,
		NetworkTxBytes:   tx,
	}, nil
}

func handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	containerID := r.PathValue("id")
	logs, err := dockerClient.ContainerLogs(r.Context(), containerID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to get logs: %v", err), http.StatusInternalServerError)
		return
	}
	defer logs.Close()

	w.Header().Set("Content-Type", "text/plain")
	io.Copy(w, logs)
}
