package matchmaking

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/andy98725/elo-service/src/api/matchResults"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
)

const (
	MATCH_MAX_DURATION = 6 * time.Hour
	GC_PAGE_SIZE       = 100
)

// ReconcileOrphanedInstances marks ServerInstance rows deleted when their
// MachineHost has already been marked deleted. Such rows can otherwise sit
// at status='starting' indefinitely — match-driven GC works through the
// Match → SI → Host chain, so once the host row is buried the SI never
// gets touched. This sweep closes that gap by walking from the SI side.
//
// Best-effort: we don't try to reach the host agent (its VM is gone), and
// we log-and-continue on individual update failures so one bad row doesn't
// stall the rest of the sweep.
func ReconcileOrphanedInstances(ctx context.Context) error {
	var orphans []models.ServerInstance
	if err := server.S.DB.
		Joins("JOIN machine_hosts mh ON mh.id = server_instances.machine_host_id").
		Where("server_instances.status != ?", models.ServerInstanceStatusDeleted).
		Where("mh.status = ?", models.MachineHostStatusDeleted).
		Find(&orphans).Error; err != nil {
		return err
	}

	for _, si := range orphans {
		slog.Info("Reconciling orphaned server instance (host deleted)",
			"instanceID", si.ID, "hostID", si.MachineHostID)

		if err := models.FreePorts(si.MachineHostID, []int64(si.HostPorts)); err != nil {
			slog.Warn("Failed to free ports for orphaned instance",
				"error", err, "instanceID", si.ID, "hostID", si.MachineHostID)
		}

		if err := models.SetServerInstanceStatus(si.ID, models.ServerInstanceStatusDeleted); err != nil {
			slog.Error("Failed to mark orphaned instance deleted",
				"error", err, "instanceID", si.ID)
		}
	}

	return nil
}

func GarbageCollectMatches(ctx context.Context) error {
	var matches []models.Match
	var nextPage int
	var err error

	for page := 0; page != -1; page = nextPage {
		if matches, nextPage, err = models.GetMatchesUnderway(page, GC_PAGE_SIZE); err != nil {
			return err
		}

		for _, match := range matches {
			if time.Since(match.CreatedAt) > MATCH_MAX_DURATION {
				slog.Info("Match timed out", "matchID", match.ID, "serverInstanceID", match.ServerInstanceID)
				if _, err := matchResults.EndMatch(ctx, &match, []string{}, "timeout", false); err != nil {
					slog.Error("Failed to end timed-out match", "error", err, "matchID", match.ID)
				}
			}
		}
	}

	return nil
}

func CleanupExpiredPlayers(ctx context.Context) error {
	keys, err := server.S.Redis.AllQueues(ctx)
	if err != nil {
		return err
	}

	for _, key := range keys {
		queueID := strings.TrimPrefix(key, "queue_")

		players, err := server.S.Redis.AllPlayersInQueue(ctx, queueID)
		if err != nil {
			return err
		}

		for _, playerID := range players {
			alive, err := server.S.Redis.IsPlayerConnectionAlive(ctx, queueID, playerID)
			if err != nil {
				slog.Info("Failed to check player queue status", "playerID", playerID, "queueID", queueID)
				continue
			}

			if !alive {
				if err := server.S.Redis.RemovePlayerFromQueue(ctx, queueID, playerID); err != nil {
					slog.Error("Failed to remove expired player from queue", "playerID", playerID, "queueID", queueID)
				} else {
					slog.Info("Removed expired player from queue", "playerID", playerID, "queueID", queueID)
				}
			}
		}
	}

	return nil
}

// CleanupExpiredLobbies sweeps lobbies whose host has gone away (TTL expired)
// and prunes member rows whose individual TTL key is gone.
func CleanupExpiredLobbies(ctx context.Context) error {
	indexKeys, err := server.S.Redis.AllLobbyIndexKeys(ctx)
	if err != nil {
		return err
	}

	for _, indexKey := range indexKeys {
		gameID := strings.TrimPrefix(indexKey, "lobby_index_")
		lobbies, err := server.S.Redis.LobbiesForGame(ctx, gameID)
		if err != nil {
			slog.Warn("Failed to read lobbies for game", "error", err, "gameID", gameID)
			continue
		}

		for _, rec := range lobbies {
			players, err := server.S.Redis.LobbyPlayers(ctx, rec.ID)
			if err != nil {
				continue
			}
			hostAlive := false
			for playerID := range players {
				alive, err := server.S.Redis.IsLobbyPlayerAlive(ctx, rec.ID, playerID)
				if err != nil {
					continue
				}
				if !alive {
					if err := server.S.Redis.RemoveLobbyPlayer(ctx, rec.ID, playerID); err != nil {
						slog.Error("Failed to remove expired lobby player", "error", err, "lobbyID", rec.ID, "playerID", playerID)
					}
					continue
				}
				if playerID == rec.HostID {
					hostAlive = true
				}
			}
			if !hostAlive {
				if err := server.S.Redis.DeleteLobby(ctx, rec.ID, rec.GameID); err != nil {
					slog.Error("Failed to delete stale lobby", "error", err, "lobbyID", rec.ID)
				} else {
					slog.Info("Deleted stale lobby", "lobbyID", rec.ID)
				}
			}
		}
	}

	return nil
}
