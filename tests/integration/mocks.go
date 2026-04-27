package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/andy98725/elo-service/src/external/hetzner"
)

// MockMachineService stands in for a real Hetzner host pool. Each "host" it
// creates is just a row in `hosts` — they all share a single in-process
// HTTP server that simulates the host agent's container endpoints
// (start/stop/health/logs). That's enough for tests because the matchmaker
// only ever calls the agent over HTTP; it doesn't introspect VMs directly.
type MockMachineService struct {
	mu        sync.Mutex
	hosts     map[string]*hetzner.HostConnectionInfo // keyed by ProviderID
	containers map[string]bool                       // containerID -> alive
	nextHost  atomic.Int64

	// CreateFn / DeleteFn let individual tests override behavior (e.g. to
	// inject errors). Nil = use the default in-memory implementation.
	CreateFn func(ctx context.Context, serverType string, agentPort int64, tls *hetzner.HostTLSOpts) (*hetzner.HostConnectionInfo, error)
	DeleteFn func(ctx context.Context, providerID string) error
	// LastTLSOpts is the TLS opts the last CreateHost call received.
	// Tests use it to assert wildcard-TLS plumbing (or to confirm absence
	// when the feature is off, in which case it stays nil).
	LastTLSOpts *hetzner.HostTLSOpts

	agentServer *http.Server
	agentPort   int
}

func NewMockMachineService() *MockMachineService {
	m := &MockMachineService{
		hosts:      make(map[string]*hetzner.HostConnectionInfo),
		containers: make(map[string]bool),
	}
	m.nextHost.Store(1000)

	mux := http.NewServeMux()

	// POST /containers — start a new game container, return a container ID.
	// DELETE /containers/<id> — stop a container.
	// GET /containers/<id>/health — 200 once the container is alive.
	// GET /containers/<id>/logs — return mock log bytes.
	mux.HandleFunc("/containers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Don't actually parse the body — real agent does, mock just acks.
		_, _ = io.Copy(io.Discard, r.Body)
		idBytes := make([]byte, 8)
		rand.Read(idBytes)
		containerID := "mock-ctr-" + hex.EncodeToString(idBytes)
		m.mu.Lock()
		m.containers[containerID] = true
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"container_id": containerID})
	})
	mux.HandleFunc("/containers/", func(w http.ResponseWriter, r *http.Request) {
		// Path looks like /containers/<id> or /containers/<id>/{health,logs}.
		rest := strings.TrimPrefix(r.URL.Path, "/containers/")
		parts := strings.SplitN(rest, "/", 2)
		containerID := parts[0]
		var sub string
		if len(parts) == 2 {
			sub = parts[1]
		}
		switch {
		case sub == "health" && r.Method == http.MethodGet:
			m.mu.Lock()
			alive := m.containers[containerID]
			m.mu.Unlock()
			if alive {
				w.WriteHeader(http.StatusOK)
			} else {
				http.Error(w, "no such container", http.StatusNotFound)
			}
		case sub == "logs" && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "text/plain")
			w.Write([]byte("mock game server logs"))
		case sub == "" && r.Method == http.MethodDelete:
			m.mu.Lock()
			delete(m.containers, containerID)
			m.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("failed to listen for mock agent server: " + err.Error())
	}
	m.agentPort = ln.Addr().(*net.TCPAddr).Port
	m.agentServer = &http.Server{Handler: mux}
	go m.agentServer.Serve(ln)

	return m
}

func (m *MockMachineService) Close() {
	if m.agentServer != nil {
		m.agentServer.Close()
	}
}

// ValidateServerType is a no-op in tests — we don't talk to Hetzner at all,
// so any name is fine.
func (m *MockMachineService) ValidateServerType(ctx context.Context, serverType string) error {
	return nil
}

func (m *MockMachineService) CreateHost(ctx context.Context, serverType string, agentPort int64, tls *hetzner.HostTLSOpts) (*hetzner.HostConnectionInfo, error) {
	m.mu.Lock()
	m.LastTLSOpts = tls
	m.mu.Unlock()

	if m.CreateFn != nil {
		return m.CreateFn(ctx, serverType, agentPort, tls)
	}

	id := m.nextHost.Add(1)
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)

	// All "hosts" share the in-process agent server — different ProviderIDs,
	// same (IP, AgentPort). That's harmless because StartContainer responds
	// uniformly and CreateMachineHost stores them as distinct DB rows.
	info := &hetzner.HostConnectionInfo{
		ProviderID: fmt.Sprintf("mock-host-%d", id),
		PublicIP:   "127.0.0.1",
		AgentPort:  int64(m.agentPort),
		AgentToken: hex.EncodeToString(tokenBytes),
	}

	m.mu.Lock()
	m.hosts[info.ProviderID] = info
	m.mu.Unlock()

	return info, nil
}

func (m *MockMachineService) DeleteHost(ctx context.Context, providerID string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, providerID)
	}

	m.mu.Lock()
	delete(m.hosts, providerID)
	m.mu.Unlock()
	return nil
}

// ActiveHosts returns the count of "live" host VMs the mock has been asked
// to create and not delete. Hosts are long-lived in the host-pool model;
// tests asserting about per-match teardown should use ActiveContainers
// instead.
func (m *MockMachineService) ActiveHosts() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.hosts)
}

// ActiveContainers returns the number of game containers the mock agent
// has been asked to start and not stop. This is the per-match counter:
// matches add a container, match-end / GC removes it.
func (m *MockMachineService) ActiveContainers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.containers)
}

type MockStorageService struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func NewMockStorageService() *MockStorageService {
	return &MockStorageService{
		objects: make(map[string][]byte),
	}
}

func (s *MockStorageService) UploadLogs(ctx context.Context, body []byte) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := fmt.Sprintf("mock-%d.log", len(s.objects))
	s.objects[key] = append([]byte(nil), body...)
	return key, nil
}

func (s *MockStorageService) GetLogs(ctx context.Context, key string) (io.ReadCloser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// PutObject and GetObject satisfy the generic blob surface used by the cert
// manager. Tests don't enable wildcard TLS, so these are unused in practice;
// they're present to satisfy the StorageService interface.
func (s *MockStorageService) PutObject(ctx context.Context, key string, body []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), body...)
	return nil
}

func (s *MockStorageService) GetObject(ctx context.Context, key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[key]
	if !ok {
		return nil, fmt.Errorf("key not found: %s", key)
	}
	return append([]byte(nil), data...), nil
}
