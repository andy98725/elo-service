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
