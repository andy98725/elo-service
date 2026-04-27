package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

var dockerClient *client.Client
var agentToken string

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
		for _, proto := range []string{"tcp", "udp"} {
			p := nat.Port(fmt.Sprintf("%d/%s", gamePort, proto))
			exposedPorts[p] = struct{}{}
			portBindings[p] = []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: fmt.Sprintf("%d", req.HostPorts[i])}}
		}
	}

	cmd := []string{"-token", req.Token}
	cmd = append(cmd, req.PlayerIDs...)

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
