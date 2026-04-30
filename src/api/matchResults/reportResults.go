package matchResults

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/andy98725/elo-service/src/worker/spectator"
	"github.com/labstack/echo"
)

type ReportResultsRequest struct {
	TokenID       string   `json:"token_id"`
	WinnerID      string   `json:"winner_id"`
	WinnerIDs     []string `json:"winner_ids"`
	Reason        string   `json:"reason"`
	AdjustRatings *bool    `json:"adjust_ratings"`
}

// ReportResults godoc
// @Summary      Report match results
// @Description  Called by the game server to report the outcome of a match
// @Tags         Results
// @Accept       json
// @Produce      json
// @Param        body body ReportResultsRequest true "Match result payload"
// @Success      200 {object} map[string]string "message"
// @Failure      400 {object} echo.HTTPError
// @Failure      404 {object} echo.HTTPError
// @Failure      409 {object} echo.HTTPError "match already ended"
// @Failure      500 {object} echo.HTTPError
// @Router       /result/report [post]
func ReportResults(c echo.Context) error {
	req := new(ReportResultsRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}
	if req.WinnerID != "" && len(req.WinnerIDs) == 0 {
		req.WinnerIDs = []string{req.WinnerID}
	}

	match, err := models.GetMatchByTokenID(req.TokenID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Match not found")
	}

	// Reject re-reports. The auth_code stays valid through cooldown so
	// game servers can finish artifact/player_data writes, but results
	// themselves are immutable once written.
	if match.Status != models.MatchStatusStarted {
		return echo.NewHTTPError(http.StatusConflict, "match already ended")
	}

	adjustRatings := true
	if req.AdjustRatings != nil {
		adjustRatings = *req.AdjustRatings
	}
	status, err := EndMatch(c.Request().Context(), match, req.WinnerIDs, req.Reason, adjustRatings)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to end match")
	}
	return c.JSON(http.StatusOK, echo.Map{"message": status})
}

// EndMatch is phase A of match completion. Synchronously: stops the
// spectator uploader, moves live spectator chunks into the replay
// prefix, writes the MatchResult, and flips the Match into cooldown
// (where the auth_code stays valid for the configured grace window so
// the game server can finish post-result work). Container teardown,
// log capture, port release, and Match-row deletion happen later in
// phase B (TeardownCooledMatch) once the cooldown window expires.
//
// When Config.MatchCooldownDuration is zero, callers should immediately
// follow EndMatch with TeardownCooledMatch — the worker sweep will
// behave identically given a zero-second window, so the synchronous
// chain is just an optimization for tests and the disable-cooldown
// case.
//
// Matches with no ServerInstance (synthetic / test path) skip the
// cooldown entirely: there is no container to keep alive and no
// auth_code work to defer, so we run a degenerate phase-A-then-B
// inline.
func EndMatch(ctx context.Context, match *models.Match, winnerIDs []string, reason string, adjustRatings bool) (string, error) {
	if isUnderway, err := models.IsMatchUnderway(match.ID); err != nil {
		return "", err
	} else if !isUnderway {
		return "", errors.New("match is not underway")
	}

	// Stop the spectator uploader before tearing the container down so
	// it doesn't issue a final poll against an agent whose container
	// just disappeared. No-op when the match wasn't streaming.
	spectator.Stop(match.ID)

	// Move live/<matchID>/* to replay/<matchID>/* and write a finalized
	// manifest. Best-effort — a failure here leaks live/ objects but
	// doesn't break match completion. Spectators that race the move
	// see one consistent prefix per request because Move writes the
	// replay manifest *after* every chunk has been copied.
	if match.SpectateEnabled {
		if err := server.S.AWS.MoveSpectateLiveToReplay(ctx, match.ID); err != nil {
			slog.Warn("Failed to move spectate stream to replay", "error", err, "matchID", match.ID)
		}
	}

	if match.ServerInstanceID == "" {
		// No container to keep alive — write the result and immediately
		// finalize. Skips the cooldown lifecycle entirely.
		if _, err := models.MatchEnded(match.ID, winnerIDs, reason, "", nil, adjustRatings); err != nil {
			slog.Error("Failed to record match result", "error", err, "matchID", match.ID)
			return "", err
		}
		if err := models.FinalizeMatchTeardown(match.ID, "", nil); err != nil {
			slog.Error("Failed to finalize match teardown", "error", err, "matchID", match.ID)
			return "", err
		}
		return "Thank you!", nil
	}

	// Phase A: write the result with empty logs/artifacts placeholders.
	// Phase B re-reads the agent and S3 index at sweep time, so anything
	// uploaded during the cooldown window still lands in MatchResult.
	if _, err := models.MatchEnded(match.ID, winnerIDs, reason, "", nil, adjustRatings); err != nil {
		slog.Error("Failed to record match result", "error", err, "matchID", match.ID)
		return "", err
	}

	// If the operator has disabled the cooldown, run phase B inline
	// against the just-cooldown'd row. The worker would do the same
	// thing on its next tick anyway; doing it here keeps tests and
	// no-cooldown deployments behaving like the pre-cooldown code.
	if server.S.Config != nil && server.S.Config.MatchCooldownDuration == 0 {
		si, err := models.GetServerInstance(match.ServerInstanceID)
		if err != nil {
			slog.Error("Failed to load server instance for inline teardown", "error", err, "matchID", match.ID)
			return "Thank you!", nil
		}
		if claimed, err := models.ClaimCooldownInstance(si.ID); err != nil || !claimed {
			// Another worker beat us; that worker will finish the work.
			return "Thank you!", nil
		}
		TeardownCooledMatch(ctx, si, &models.Match{ID: match.ID}, false)
		return "Thank you!", nil
	}

	return "Thank you!", nil
}

// TeardownCooledMatch is phase B of match completion: stops the
// container, frees ports, marks the SI deleted, finalizes the
// MatchResult with the now-final logs key and artifact list, deletes
// the Match row, and (best-effort) reaps the host if it's now idle.
//
// The caller MUST have already won the cooldown claim via
// ClaimCooldownInstance (the SI status is already 'tearing_down').
// On agent-stop failure with force=false, the SI is reverted to
// 'cooldown' so the next sweep retries; with force=true (caller has
// hit the force deadline) the function proceeds with port release
// and Match deletion regardless, leaving any actual container alive
// on the host until ReconcileLiveHosts/ReconcileOrphanedInstances
// catches it.
func TeardownCooledMatch(ctx context.Context, si *models.ServerInstance, match *models.Match, force bool) {
	matchID := match.ID

	logsKey, err := saveMatchLogs(ctx, si)
	if err != nil {
		slog.Warn("Failed to save match logs", "error", err, "matchID", matchID)
		logsKey = ""
	}

	// Re-read the artifact index so anything uploaded during the
	// cooldown window appears in the final MatchResult. Best-effort:
	// a storage hiccup here keeps whatever phase A set (which is
	// empty in the new flow, but defending the contract anyway).
	var artifactNames []string
	if index, err := server.S.AWS.GetMatchArtifactIndex(ctx, matchID); err != nil {
		slog.Warn("Failed to read artifact index at teardown", "error", err, "matchID", matchID)
	} else {
		artifactNames = make([]string, 0, len(index))
		for name := range index {
			artifactNames = append(artifactNames, name)
		}
	}

	stopErr := hetzner.StopContainer(ctx,
		si.MachineHost.PublicIP, si.MachineHost.AgentPort, si.MachineHost.AgentToken,
		si.ContainerID, si.SpectateID)
	if stopErr != nil {
		slog.Error("Failed to stop game container during cooldown sweep", "error", stopErr,
			"containerID", si.ContainerID, "hostID", si.MachineHostID, "force", force)
		if !force {
			// Revert so the next sweep retries.
			if err := models.RevertTearingDownToCooldown(si.ID); err != nil {
				slog.Error("Failed to revert SI to cooldown after stop failure", "error", err, "instanceID", si.ID)
			}
			return
		}
		// force=true: continue with the rest of teardown so we don't
		// leak ports / DB rows. The container itself may keep running
		// on the host; the host-level reconcilers will catch it.
	}

	if err := models.FreePorts(si.MachineHostID, []int64(si.HostPorts)); err != nil {
		slog.Error("Failed to free ports", "error", err, "hostID", si.MachineHostID)
	}

	if err := models.SetServerInstanceStatus(si.ID, models.ServerInstanceStatusDeleted); err != nil {
		slog.Error("Failed to mark server instance deleted", "error", err, "instanceID", si.ID)
	}

	if err := models.FinalizeMatchTeardown(matchID, logsKey, artifactNames); err != nil {
		slog.Error("Failed to finalize match teardown", "error", err, "matchID", matchID)
	}

	go tryDeleteIdleHost(&si.MachineHost)
}

// tryDeleteIdleHost deletes the host VM if it has no remaining active
// instances and removing it would not drop available slots below the warm
// pool target. Runs in a goroutine so it never blocks the match-end
// response.
func tryDeleteIdleHost(host *models.MachineHost) {
	count, err := models.CountActiveInstancesOnHost(host.ID)
	if err != nil || count > 0 {
		return
	}

	if warmSlots := server.S.Config.HCLOUDWarmSlots; warmSlots > 0 {
		available, err := models.CountAvailableSlots()
		if err != nil {
			slog.Error("Failed to count available slots; skipping host deletion", "error", err, "hostID", host.ID)
			return
		}
		// available already includes this host's slots; check if we can
		// afford to lose them
		if available-int64(host.MaxSlots) < int64(warmSlots) {
			slog.Info("Keeping idle host to maintain warm pool", "hostID", host.ID,
				"available", available, "warmSlots", warmSlots)
			return
		}
	}

	if err := models.SetMachineHostDeleted(host.ID); err != nil {
		slog.Error("Failed to mark host deleted in DB", "error", err, "hostID", host.ID)
		return
	}

	// Best-effort DNS cleanup before the VM goes away. A stale record
	// outlives its 60s TTL anyway, so this isn't fatal.
	if server.S.DNS != nil && host.DNSRecordID != "" {
		if err := server.S.DNS.DeleteARecord(context.Background(), host.DNSRecordID); err != nil {
			slog.Warn("Failed to delete DNS record (will leak until TTL)",
				"error", err, "hostID", host.ID, "recordID", host.DNSRecordID)
		}
	}

	if err := server.S.Machines.DeleteHost(context.Background(), host.ProviderID); err != nil {
		slog.Error("Failed to delete idle host VM", "error", err, "providerID", host.ProviderID)
	} else {
		slog.Info("Deleted idle host VM", "providerID", host.ProviderID)
	}
}
