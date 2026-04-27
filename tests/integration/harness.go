package integration

import (
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/andy98725/elo-service/src/api"
	extredis "github.com/andy98725/elo-service/src/external/redis"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type Harness struct {
	T         *testing.T
	Server    *httptest.Server
	Echo      *echo.Echo
	Mini      *miniredis.Miniredis
	Machines  *MockMachineService
	Storage   *MockStorageService
	WorkerCtx context.Context
	cancelFn  context.CancelFunc
}

// NewHarness boots a fresh in-process server (miniredis + SQLite + mocks).
//
// Tests using this harness MUST NOT call t.Parallel(): the harness reassigns
// the package-global server.S, and concurrent assignments would race and
// cross-contaminate state between tests. The default serial test ordering is
// what makes this safe.
func NewHarness(t *testing.T) *Harness {
	t.Helper()

	os.Setenv("JWT_SECRET_KEY", "test-secret-key-for-integration-tests")

	mini, err := miniredis.Run()
	if err != nil {
		t.Fatalf("failed to start miniredis: %v", err)
	}

	redisClient, err := extredis.NewRedis(fmt.Sprintf("redis://%s", mini.Addr()))
	if err != nil {
		t.Fatalf("failed to connect to miniredis: %v", err)
	}

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}

	if err := migrateForSQLite(db); err != nil {
		t.Fatalf("failed to migrate: %v", err)
	}

	machines := NewMockMachineService()
	storage := NewMockStorageService()

	e := echo.New()
	e.Use(middleware.Recover())

	server.S = &server.Server{
		Config: &server.Config{
			Port:                          "0",
			MatchmakingPairingMinInterval: 10 * time.Millisecond,
			MatchmakingGCMinInterval:      10 * time.Millisecond,
			Endpoint:                      "http://localhost",
			// Host-pool sizing. Numbers are tight so tests can exercise the
			// "create new host" path quickly, but high enough that no real
			// scenario hits the cap.
			HCLOUDMaxHosts:        4,
			HCLOUDMaxSlotsPerHost: 4,
			HCLOUDPortRangeStart:  10000,
			HCLOUDPortRangeEnd:    11000,
			HCLOUDAgentPort:       8080,
			HCLOUDHostType:        "cx23",
		},
		Logger:   slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		DB:       db,
		Redis:    redisClient,
		AWS:      storage,
		Machines: machines,
		Shutdown: make(chan struct{}),
	}

	if err := api.InitRoutes(e); err != nil {
		t.Fatalf("failed to init routes: %v", err)
	}

	ts := httptest.NewServer(e)

	ctx, cancel := context.WithCancel(context.Background())
	go worker.RunWorker(ctx, server.S.Shutdown)

	// Wait until the worker has actually subscribed to the matchmaking
	// trigger channel. Without this, a test that calls TriggerMatchmaking
	// immediately after NewHarness can race the worker's SUBSCRIBE and
	// the publish lands with no listener — the worker never wakes and the
	// match is never paired, surfacing as a flaky 5-second timeout.
	waitForSubscriber(t, redisClient, extredis.MatchmakingTriggerChannel, 2*time.Second)

	h := &Harness{
		T:         t,
		Server:    ts,
		Echo:      e,
		Mini:      mini,
		Machines:  machines,
		Storage:   storage,
		WorkerCtx: ctx,
		cancelFn:  cancel,
	}

	t.Cleanup(func() {
		cancel()
		close(server.S.Shutdown)
		ts.Close()
		machines.Close()
		mini.Close()
	})

	return h
}

func (h *Harness) BaseURL() string {
	return h.Server.URL
}

func waitForSubscriber(t *testing.T, r *extredis.Redis, channel string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		counts, err := r.Client.PubSubNumSub(context.Background(), channel).Result()
		if err == nil && counts[channel] >= 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("worker did not subscribe to %q within %v", channel, timeout)
}
