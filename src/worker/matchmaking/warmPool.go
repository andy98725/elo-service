package matchmaking

import (
	"context"
	"log/slog"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

// MaintainWarmPool ensures at least HCLOUDWarmSlots container slots are
// available across ready VMs. It provisions new VMs as needed, up to
// HCLOUDMaxHosts. A no-op when HCLOUDWarmSlots is 0.
func MaintainWarmPool(ctx context.Context) error {
	cfg := server.S.Config
	if cfg.HCLOUDWarmSlots <= 0 {
		return nil
	}

	available, err := models.CountAvailableSlots()
	if err != nil {
		return err
	}

	for available < int64(cfg.HCLOUDWarmSlots) {
		count, err := models.CountMachineHosts()
		if err != nil {
			return err
		}
		if count >= int64(cfg.HCLOUDMaxHosts) {
			slog.Warn("Warm pool: cannot reach target slots, at VM cap",
				"available", available, "target", cfg.HCLOUDWarmSlots, "vmCap", cfg.HCLOUDMaxHosts)
			break
		}

		slog.Info("Warm pool: provisioning VM to meet slot target",
			"available", available, "target", cfg.HCLOUDWarmSlots)

		connInfo, err := server.S.Machines.CreateHost(ctx, cfg.HCLOUDHostType, cfg.HCLOUDAgentPort)
		if err != nil {
			slog.Error("Warm pool: failed to provision VM", "error", err)
			return err
		}

		host, err := models.CreateMachineHost(
			connInfo.ProviderID, connInfo.PublicIP, connInfo.AgentToken,
			connInfo.AgentPort, cfg.HCLOUDMaxSlotsPerHost,
		)
		if err != nil {
			slog.Error("Warm pool: failed to save host to DB; cleaning up VM", "error", err)
			server.S.Machines.DeleteHost(context.Background(), connInfo.ProviderID)
			return err
		}

		if err := models.SetMachineHostReady(host.ID); err != nil {
			slog.Error("Warm pool: failed to mark host ready", "error", err)
			return err
		}

		slog.Info("Warm pool: VM ready", "hostID", host.ID, "ip", connInfo.PublicIP)
		available += int64(cfg.HCLOUDMaxSlotsPerHost)
	}

	return nil
}
