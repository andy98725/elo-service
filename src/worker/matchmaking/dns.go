package matchmaking

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

// buildHostTLSOpts returns the cloud-init TLS opts for a new host, or nil
// when the wildcard-TLS feature is disabled. Centralizes the "fetch the
// current cert + bundle the port range" step that both StartMatch and the
// warm pool need.
func buildHostTLSOpts() (*hetzner.HostTLSOpts, error) {
	if server.S.Cert == nil {
		return nil, nil
	}
	cert, key, err := server.S.Cert.CurrentPEM()
	if err != nil {
		return nil, fmt.Errorf("read wildcard cert: %w", err)
	}
	cfg := server.S.Config
	return &hetzner.HostTLSOpts{
		CertPEM:        cert,
		KeyPEM:         key,
		PortRangeStart: cfg.HCLOUDPortRangeStart,
		PortRangeEnd:   cfg.HCLOUDPortRangeEnd,
	}, nil
}

// provisionDNSForHost creates a per-host A record once the host VM is up
// and persists the hostname/recordID on the MachineHost row. No-op when
// the wildcard-TLS feature isn't configured (server.S.DNS == nil).
//
// Failures here don't roll back the host: the host stays in the pool and
// serves IP-based connections (degraded — WebGL clients can't TLS-connect
// to that host until a follow-up sweep retries DNS). The matchmaker prefers
// hostname-equipped hosts when picking match.ConnectionAddress, so a
// degraded host eventually drains and the next warm-pool refill replaces it.
func provisionDNSForHost(ctx context.Context, host *models.MachineHost, publicIP string) {
	if server.S.DNS == nil {
		return
	}
	if host.PublicHostname != "" {
		return // already provisioned (e.g. host row reused after restart)
	}

	hostname := fmt.Sprintf("host-%s.%s", host.ID, server.S.Config.GameServerDomain)
	recordID, err := server.S.DNS.CreateARecord(ctx, hostname, publicIP)
	if err != nil {
		slog.Warn("DNS provisioning failed for host (degraded; clients will use IP)",
			"error", err, "hostID", host.ID, "hostname", hostname)
		return
	}

	if err := models.SetMachineHostHostname(host.ID, hostname, recordID); err != nil {
		// Record exists in Cloudflare but we couldn't persist the link.
		// Best-effort cleanup: delete the orphan record, log, move on.
		slog.Error("Failed to save DNS hostname; cleaning up record",
			"error", err, "hostID", host.ID, "recordID", recordID)
		_ = server.S.DNS.DeleteARecord(context.Background(), recordID)
		return
	}

	host.PublicHostname = hostname
	host.DNSRecordID = recordID
	slog.Info("DNS record created for host", "hostID", host.ID, "hostname", hostname, "ip", publicIP)
}

// teardownDNSForHost deletes the per-host A record when the host is being
// decommissioned. Best-effort: a stale record outlives its TTL anyway.
func teardownDNSForHost(ctx context.Context, host *models.MachineHost) {
	if server.S.DNS == nil || host.DNSRecordID == "" {
		return
	}
	if err := server.S.DNS.DeleteARecord(ctx, host.DNSRecordID); err != nil {
		slog.Warn("Failed to delete DNS record (will leak until TTL expiry)",
			"error", err, "hostID", host.ID, "hostname", host.PublicHostname, "recordID", host.DNSRecordID)
	}
}
