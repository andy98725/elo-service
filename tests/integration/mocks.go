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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/andy98725/elo-service/src/external/aws"
	"github.com/andy98725/elo-service/src/external/hetzner"
)

// MockMachineService stands in for a real Hetzner host pool. Each "host" it
// creates is just a row in `hosts` — they all share a single in-process
// HTTP server that simulates the host agent's container endpoints
// (start/stop/health/logs). That's enough for tests because the matchmaker
// only ever calls the agent over HTTP; it doesn't introspect VMs directly.
// MockSpectateBuffer is the per-spectate-id append-only byte buffer the
// mock agent serves. Tests push bytes into a buffer to simulate a game
// server writing to /shared/spectate.stream; the matchmaker's uploader
// then pulls from `/spectate/<id>` and the uploader chunks land in the
// MockStorageService. Lets tests assert on the upload flow end-to-end
// without a real game container.
type MockSpectateBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *MockSpectateBuffer) Append(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
}

func (b *MockSpectateBuffer) bytesFrom(offset int64, max int) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	if offset >= int64(len(b.buf)) {
		return nil
	}
	end := int64(len(b.buf))
	if max > 0 && offset+int64(max) < end {
		end = offset + int64(max)
	}
	out := make([]byte, end-offset)
	copy(out, b.buf[offset:end])
	return out
}

type MockMachineService struct {
	mu        sync.Mutex
	hosts     map[string]*hetzner.HostConnectionInfo // keyed by ProviderID
	containers map[string]bool                       // containerID -> alive
	nextHost  atomic.Int64

	// spectateBuffers is keyed by the spectate_id the matchmaker generates
	// when starting a container. Tests grab a buffer via SpectateBuffer
	// and Append bytes to simulate a game-server stream; the mock agent's
	// /spectate/<id> route reads from it.
	spectateBuffers map[string]*MockSpectateBuffer

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
		hosts:           make(map[string]*hetzner.HostConnectionInfo),
		containers:      make(map[string]bool),
		spectateBuffers: make(map[string]*MockSpectateBuffer),
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
		// Parse the payload so we can register the spectate_id buffer up
		// front — the matchmaker uploader expects /spectate/<id> to be
		// reachable as soon as the container is "started."
		var req struct {
			SpectateID string `json:"spectate_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		idBytes := make([]byte, 8)
		rand.Read(idBytes)
		containerID := "mock-ctr-" + hex.EncodeToString(idBytes)
		m.mu.Lock()
		m.containers[containerID] = true
		if req.SpectateID != "" {
			if _, ok := m.spectateBuffers[req.SpectateID]; !ok {
				m.spectateBuffers[req.SpectateID] = &MockSpectateBuffer{}
			}
		}
		m.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"container_id": containerID})
	})

	mux.HandleFunc("/spectate/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/spectate/")
		m.mu.Lock()
		buf := m.spectateBuffers[id]
		m.mu.Unlock()
		if buf == nil {
			// Mirror the real agent: missing dir returns 200 + empty.
			w.WriteHeader(http.StatusOK)
			return
		}
		offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
		max := 1 << 18
		if v := r.URL.Query().Get("max"); v != "" {
			if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
				max = parsed
			}
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(buf.bytesFrom(offset, max))
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

// ListHosts returns the provider IDs of every host the mock currently
// considers alive. Tests can simulate an out-of-band VM deletion by
// mutating the hosts map directly via VanishHost — the next
// ReconcileLiveHosts call will then see the row as orphaned.
func (m *MockMachineService) ListHosts(ctx context.Context) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.hosts))
	for id := range m.hosts {
		out = append(out, id)
	}
	return out, nil
}

// VanishHost removes a host from the mock's live set without going
// through DeleteHost — simulates the production scenario where a VM is
// destroyed via the Hetzner console (or any other out-of-band path)
// and the matchmaker DB doesn't know.
func (m *MockMachineService) VanishHost(providerID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.hosts, providerID)
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

// SpectateBuffer returns the per-spectate-id buffer the mock agent serves.
// Tests use it to push bytes into the mock stream so the matchmaker's
// uploader has something to chunk into S3. Returns nil when no container
// has been started with that ID yet.
func (m *MockMachineService) SpectateBuffer(spectateID string) *MockSpectateBuffer {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.spectateBuffers[spectateID]
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
	mu sync.Mutex
	// objects backs UploadLogs/GetLogs/PutObject/GetObject and stores
	// every JSON-shaped sidecar (manifests, indices, etc.) by full key.
	objects map[string][]byte
	// artifactBlobs is the per-(match, artifact-name) raw-bytes store
	// for PutMatchArtifact/GetMatchArtifact. Kept separate so we can
	// also remember the original Content-Type per artifact (S3 returns
	// it as object metadata; the in-memory map carries it explicitly).
	artifactBlobs map[string]mockArtifactBlob
}

func NewMockStorageService() *MockStorageService {
	return &MockStorageService{
		objects:       make(map[string][]byte),
		artifactBlobs: make(map[string]mockArtifactBlob),
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

func (s *MockStorageService) PutSpectateChunk(ctx context.Context, matchID string, seq int, data []byte) error {
	key := fmt.Sprintf("live/%s/%d.bin", matchID, seq)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), data...)
	return nil
}

func (s *MockStorageService) PutSpectateManifest(ctx context.Context, matchID string, manifest []byte) error {
	key := fmt.Sprintf("live/%s/manifest.json", matchID)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = append([]byte(nil), manifest...)
	return nil
}

func (s *MockStorageService) GetSpectateManifest(ctx context.Context, matchID string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.objects[fmt.Sprintf("replay/%s/manifest.json", matchID)]; ok {
		return append([]byte(nil), data...), nil
	}
	if data, ok := s.objects[fmt.Sprintf("live/%s/manifest.json", matchID)]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, aws.ErrNotFound
}

func (s *MockStorageService) GetSpectateChunk(ctx context.Context, matchID string, seq int) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.objects[fmt.Sprintf("replay/%s/%d.bin", matchID, seq)]; ok {
		return append([]byte(nil), data...), nil
	}
	if data, ok := s.objects[fmt.Sprintf("live/%s/%d.bin", matchID, seq)]; ok {
		return append([]byte(nil), data...), nil
	}
	return nil, aws.ErrNotFound
}

// mockArtifactBlob is the per-artifact value the mock storage stashes —
// content-type alongside the bytes, since S3 returns Content-Type from
// object metadata and tests need to round-trip it.
type mockArtifactBlob struct {
	body        []byte
	contentType string
}

func (s *MockStorageService) PutMatchArtifact(ctx context.Context, matchID, name, contentType string, body []byte) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.artifactBlobs == nil {
		s.artifactBlobs = map[string]mockArtifactBlob{}
	}
	objKey := fmt.Sprintf("artifacts/%s/%s", matchID, name)
	s.artifactBlobs[objKey] = mockArtifactBlob{
		body:        append([]byte(nil), body...),
		contentType: contentType,
	}

	indexKey := fmt.Sprintf("artifacts/%s/index.json", matchID)
	index := map[string]aws.MatchArtifactMeta{}
	if existing, ok := s.objects[indexKey]; ok {
		_ = json.Unmarshal(existing, &index)
	}
	index[name] = aws.MatchArtifactMeta{
		ContentType: contentType,
		SizeBytes:   int64(len(body)),
		UploadedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		return err
	}
	s.objects[indexKey] = indexBytes
	return nil
}

func (s *MockStorageService) GetMatchArtifact(ctx context.Context, matchID, name string) ([]byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objKey := fmt.Sprintf("artifacts/%s/%s", matchID, name)
	blob, ok := s.artifactBlobs[objKey]
	if !ok {
		return nil, "", aws.ErrNotFound
	}
	return append([]byte(nil), blob.body...), blob.contentType, nil
}

func (s *MockStorageService) GetMatchArtifactIndex(ctx context.Context, matchID string) (map[string]aws.MatchArtifactMeta, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	indexKey := fmt.Sprintf("artifacts/%s/index.json", matchID)
	data, ok := s.objects[indexKey]
	if !ok {
		return map[string]aws.MatchArtifactMeta{}, nil
	}
	out := map[string]aws.MatchArtifactMeta{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *MockStorageService) MoveSpectateLiveToReplay(ctx context.Context, matchID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	manifestKey := fmt.Sprintf("live/%s/manifest.json", matchID)
	manifestBytes, ok := s.objects[manifestKey]
	if !ok {
		return nil
	}
	var m struct {
		MatchID    string `json:"match_id"`
		StartedAt  string `json:"started_at"`
		LatestSeq  int    `json:"latest_seq"`
		ChunkCount int    `json:"chunk_count"`
		Finalized  bool   `json:"finalized"`
	}
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return err
	}

	for seq := 0; seq < m.ChunkCount; seq++ {
		liveKey := fmt.Sprintf("live/%s/%d.bin", matchID, seq)
		replayKey := fmt.Sprintf("replay/%s/%d.bin", matchID, seq)
		if data, exists := s.objects[liveKey]; exists {
			s.objects[replayKey] = append([]byte(nil), data...)
		}
	}
	m.Finalized = true
	finalized, err := json.Marshal(m)
	if err != nil {
		return err
	}
	s.objects[fmt.Sprintf("replay/%s/manifest.json", matchID)] = finalized
	for seq := 0; seq < m.ChunkCount; seq++ {
		delete(s.objects, fmt.Sprintf("live/%s/%d.bin", matchID, seq))
	}
	delete(s.objects, manifestKey)
	return nil
}

// SpectateObjectKeys returns sorted keys under the given prefix. Lets
// tests assert on what the uploader put in S3 without poking at internal
// fields.
func (s *MockStorageService) SpectateObjectKeys(prefix string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0)
	for k := range s.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// SpectateObject returns the bytes for a stored key, or nil if not present.
func (s *MockStorageService) SpectateObject(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data, ok := s.objects[key]; ok {
		return append([]byte(nil), data...)
	}
	return nil
}
