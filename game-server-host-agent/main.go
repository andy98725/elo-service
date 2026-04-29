package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
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
	// SpectateID names a host-side directory the agent mounts into the
	// container at /shared/. Game servers that opt into spectating write
	// to /shared/spectate.stream; the matchmaker pulls bytes from this
	// agent at /spectate/<spectate_id>. Generated and provided by the
	// matchmaker; the agent only validates path safety.
	SpectateID string `json:"spectate_id"`
}

type startContainerResponse struct {
	ContainerID string `json:"container_id"`
}

// spectateDirRoot is the host-side root under which each container's
// shared/ mount lives. Mounted into the agent's own container by
// cloud-init at the same path so the agent can read the file the game
// server writes. Distinct from container stdout / log retrieval — the
// spectator stream is a separate pipe.
const spectateDirRoot = "/var/lib/elo-spectate"

// spectateFileName is the file the game server is expected to append to
// inside the mounted /shared/ directory. Deliberately not `.log` to
// reinforce that this stream is independent of the existing log pipe.
const spectateFileName = "spectate.stream"

// safeSpectateIDPattern rejects values containing anything other than
// basic identifier characters — catches path-traversal attempts before
// they reach the host filesystem.
var safeSpectateIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func spectatePath(id string) (string, error) {
	if !safeSpectateIDPattern.MatchString(id) {
		return "", errors.New("invalid spectate_id")
	}
	return filepath.Join(spectateDirRoot, id), nil
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
	mux.HandleFunc("GET /spectate/{id}", handleSpectate)

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

	// Always create the spectator dir and bind it into /shared/, even
	// for non-spectate matches: the game-server contract is uniform
	// (write to /shared/spectate.stream if you want streaming), and an
	// empty dir per container is essentially free. Whether the matchmaker
	// actually pulls from /spectate/<id> is decided by Match.SpectateEnabled.
	mounts := []mount.Mount{}
	if req.SpectateID != "" {
		dir, err := spectatePath(req.SpectateID)
		if err != nil {
			http.Error(w, "invalid spectate_id", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, fmt.Sprintf("failed to create spectate dir: %v", err), http.StatusInternalServerError)
			return
		}
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: dir,
			Target: "/shared",
		})
	}

	// TODO: add Resources (Memory, NanoCPUs) to HostConfig once /containers/stats
	// gives us per-game footprint numbers from a real load test. Today every
	// container on a host shares the VM's CPU/RAM unbounded — a noisy game can
	// starve its neighbors. Right-size from measurements before raising slot
	// counts.
	created, err := dockerClient.ContainerCreate(ctx,
		&container.Config{Image: req.Image, Cmd: cmd, ExposedPorts: exposedPorts},
		&container.HostConfig{PortBindings: portBindings, Mounts: mounts},
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

	// If the matchmaker tells us the spectate_id, clean up the host-side
	// dir so we don't leak storage. Best-effort: missing dir is fine
	// (older containers / non-spectate matches).
	if sid := r.URL.Query().Get("spectate_id"); sid != "" {
		if dir, err := spectatePath(sid); err == nil {
			os.RemoveAll(dir)
		}
	}

	w.WriteHeader(http.StatusOK)
}

// handleSpectate serves a slice of /var/lib/elo-spectate/<id>/spectate.stream
// starting at ?offset=N for at most ?max=M bytes. Returns 200 with the
// raw bytes (or empty body when caught up). Returns 404 when the
// spectate dir is missing — usually means the match isn't streaming or
// has been torn down. The matchmaker is the only authenticated caller.
func handleSpectate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	dir, err := spectatePath(id)
	if err != nil {
		http.Error(w, "invalid spectate id", http.StatusBadRequest)
		return
	}

	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if offset < 0 {
		offset = 0
	}
	const defaultMax = 1 << 18 // 256 KiB
	max := int64(defaultMax)
	if v := r.URL.Query().Get("max"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 && parsed < (1<<24) {
			max = parsed
		}
	}

	path := filepath.Join(dir, spectateFileName)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Either the dir doesn't exist (no streaming match) or the
			// game server hasn't written the file yet. Return 200 + empty
			// so the matchmaker poll loop just records "no new bytes
			// this tick" rather than treating it as fatal.
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, fmt.Sprintf("failed to open spectate stream: %v", err), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		http.Error(w, fmt.Sprintf("failed to seek: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.WriteHeader(http.StatusOK)
	io.CopyN(w, f, max)
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
