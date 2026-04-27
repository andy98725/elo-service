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
	CreateHost(ctx context.Context, serverType string, agentPort int64) (*hetzner.HostConnectionInfo, error)
	DeleteHost(ctx context.Context, providerID string) error
}

type StorageService interface {
	UploadLogs(ctx context.Context, body []byte) (string, error)
	GetLogs(ctx context.Context, key string) (io.ReadCloser, error)
}
