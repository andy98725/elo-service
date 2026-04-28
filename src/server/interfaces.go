package server

import (
	"context"
	"io"

	"github.com/andy98725/elo-service/src/external/hetzner"
)

// MachineService manages long-lived host VMs that run multiple game
// containers each. The matchmaker provisions hosts on demand (or warmly
// via the warm pool) and tears them down through this interface.
type MachineService interface {
	// ValidateServerType is called at startup so a misconfigured
	// HCLOUD_HOST_TYPE fails loudly instead of silently at first match.
	ValidateServerType(ctx context.Context, serverType string) error
	// CreateHost provisions a new host VM. When `tls` is non-nil, the
	// host is brought up with Caddy + the wildcard cert pre-installed.
	CreateHost(ctx context.Context, serverType string, agentPort int64, tls *hetzner.HostTLSOpts) (*hetzner.HostConnectionInfo, error)
	DeleteHost(ctx context.Context, providerID string) error
	// ListHosts returns the provider IDs of every game-host VM currently
	// alive at the provider. Used by ReconcileLiveHosts to detect DB rows
	// whose underlying VM was destroyed out-of-band.
	ListHosts(ctx context.Context) ([]string, error)
}

type StorageService interface {
	UploadLogs(ctx context.Context, body []byte) (string, error)
	GetLogs(ctx context.Context, key string) (io.ReadCloser, error)

	// Generic blob get/put used by the cert manager. Implementations
	// signal "key does not exist" via an error that the cert manager
	// detects through the AWSClient.ErrNotFound sentinel.
	PutObject(ctx context.Context, key string, body []byte) error
	GetObject(ctx context.Context, key string) ([]byte, error)
}

// DNSService is the per-host DNS-record CRUD surface. Production is satisfied
// by cloudflare.Client; tests pass a no-op or in-memory mock.
type DNSService interface {
	CreateARecord(ctx context.Context, name, ip string) (recordID string, err error)
	DeleteARecord(ctx context.Context, recordID string) error
	FindARecordByName(ctx context.Context, name string) (recordID string, err error)
}

// CertService exposes the current wildcard cert+key. Cloud-init reads this
// when building a host's user-data. Returns an error if no cert is loaded
// yet (caller must wait or skip).
type CertService interface {
	CurrentPEM() (cert, key []byte, err error)
	// EnsureFresh is invoked periodically by the worker to renew the cert
	// before it expires. Implementations must be idempotent — a no-op when
	// the current cert has plenty of life left.
	EnsureFresh(ctx context.Context) error
}
