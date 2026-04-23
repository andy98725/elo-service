package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/andy98725/elo-service/src/external/hetzner"
)

type MockMachineService struct {
	mu       sync.Mutex
	servers  map[int64]*hetzner.MachineConnectionInfo
	nextID   atomic.Int64
	CreateFn func(ctx context.Context, config *hetzner.MachineConfig) (*hetzner.MachineConnectionInfo, error)
	DeleteFn func(ctx context.Context, machineID int64, machineName string) error

	healthServer *http.Server
	healthPort   int
}

func NewMockMachineService() *MockMachineService {
	m := &MockMachineService{
		servers: make(map[int64]*hetzner.MachineConnectionInfo),
	}
	m.nextID.Store(1000)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/logs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("mock game server logs"))
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic("failed to listen for mock health server: " + err.Error())
	}
	m.healthPort = ln.Addr().(*net.TCPAddr).Port
	m.healthServer = &http.Server{Handler: mux}
	go m.healthServer.Serve(ln)

	return m
}

func (m *MockMachineService) Close() {
	if m.healthServer != nil {
		m.healthServer.Close()
	}
}

func (m *MockMachineService) CreateServer(ctx context.Context, config *hetzner.MachineConfig) (*hetzner.MachineConnectionInfo, error) {
	if m.CreateFn != nil {
		return m.CreateFn(ctx, config)
	}

	id := m.nextID.Add(1)
	tokenBytes := make([]byte, 16)
	rand.Read(tokenBytes)

	info := &hetzner.MachineConnectionInfo{
		MachineName: fmt.Sprintf("mock-server-%d", id),
		MachineID:   id,
		AuthCode:    hex.EncodeToString(tokenBytes),
		PublicIP:    "127.0.0.1",
		LogsPort:    int64(m.healthPort),
	}

	m.mu.Lock()
	m.servers[id] = info
	m.mu.Unlock()

	return info, nil
}

func (m *MockMachineService) DeleteServer(ctx context.Context, machineID int64, machineName string) error {
	if m.DeleteFn != nil {
		return m.DeleteFn(ctx, machineID, machineName)
	}

	m.mu.Lock()
	delete(m.servers, machineID)
	m.mu.Unlock()
	return nil
}

func (m *MockMachineService) ActiveServers() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.servers)
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
