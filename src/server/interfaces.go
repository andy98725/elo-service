package server

import (
	"context"
	"io"

	"github.com/andy98725/elo-service/src/external/hetzner"
)

type MachineService interface {
	CreateServer(ctx context.Context, config *hetzner.MachineConfig) (*hetzner.MachineConnectionInfo, error)
	DeleteServer(ctx context.Context, machineID int64, machineName string) error
}

type StorageService interface {
	UploadLogs(ctx context.Context, body []byte) (string, error)
	GetLogs(ctx context.Context, key string) (io.ReadCloser, error)
}
