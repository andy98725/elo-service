package matchResults

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
)

type ReportResultsRequest struct {
	TokenID   string   `json:"token_id"`
	WinnerID  string   `json:"winner_id"`
	WinnerIDs []string `json:"winner_ids"`
	Reason    string   `json:"reason"`
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

	status, err := EndMatch(c.Request().Context(), match, req.WinnerIDs, req.Reason)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to end match")
	}
	return c.JSON(http.StatusOK, echo.Map{"message": status})
}

func EndMatch(ctx context.Context, match *models.Match, winnerIDs []string, reason string) (string, error) {
	if isUnderway, err := models.IsMatchUnderway(match.ID); err != nil {
		return "", err
	} else if !isUnderway {
		return "", errors.New("match is not underway")
	}

	status := ""

	if match.ServerInstanceID != "" {
		si, err := models.GetServerInstance(match.ServerInstanceID)
		if err != nil {
			slog.Error("Failed to load server instance for match", "error", err, "matchID", match.ID)
			return "", err
		}

		logsKey, err := saveMatchLogs(ctx, si)
		if err != nil {
			slog.Warn("Failed to save match logs", "error", err, "matchID", match.ID)
			logsKey = ""
			status = "Failed to save match logs"
		}

		if _, err := models.MatchEnded(match.ID, winnerIDs, reason, logsKey); err != nil {
			slog.Error("Failed to record match result", "error", err, "matchID", match.ID)
			return "", err
		}

		if err := hetzner.StopContainer(ctx,
			si.MachineHost.PublicIP, si.MachineHost.AgentPort, si.MachineHost.AgentToken,
			si.ContainerID); err != nil {
			slog.Error("Failed to stop game container; it may still be running", "error", err,
				"containerID", si.ContainerID, "hostID", si.MachineHostID)
		}

		if err := models.FreePorts(si.MachineHostID, []int64(si.HostPorts)); err != nil {
			slog.Error("Failed to free ports", "error", err, "hostID", si.MachineHostID)
		}

		if err := models.SetServerInstanceStatus(si.ID, models.ServerInstanceStatusDeleted); err != nil {
			slog.Error("Failed to mark server instance deleted", "error", err, "instanceID", si.ID)
		}

		go tryDeleteIdleHost(si.MachineHostID, si.MachineHost.ProviderID)
	} else {
		if _, err := models.MatchEnded(match.ID, winnerIDs, reason, ""); err != nil {
			slog.Error("Failed to record match result", "error", err, "matchID", match.ID)
			return "", err
		}
	}

	if status == "" {
		status = "Thank you!"
	}
	return status, nil
}

// tryDeleteIdleHost deletes the host VM if it has no remaining active instances
// and removing it would not drop available slots below the warm pool target.
// Runs in a goroutine so it never blocks the match-end response.
func tryDeleteIdleHost(hostID, providerID string) {
	count, err := models.CountActiveInstancesOnHost(hostID)
	if err != nil || count > 0 {
		return
	}

	if warmSlots := server.S.Config.HCLOUDWarmSlots; warmSlots > 0 {
		available, err := models.CountAvailableSlots()
		if err != nil {
			slog.Error("Failed to count available slots; skipping host deletion", "error", err, "hostID", hostID)
			return
		}
		host, err := models.GetMachineHost(hostID)
		if err != nil {
			slog.Error("Failed to load host; skipping host deletion", "error", err, "hostID", hostID)
			return
		}
		// available already includes this host's slots; check if we can afford to lose them
		if available-int64(host.MaxSlots) < int64(warmSlots) {
			slog.Info("Keeping idle host to maintain warm pool", "hostID", hostID,
				"available", available, "warmSlots", warmSlots)
			return
		}
	}

	if err := models.SetMachineHostDeleted(hostID); err != nil {
		slog.Error("Failed to mark host deleted in DB", "error", err, "hostID", hostID)
		return
	}

	if err := server.S.Machines.DeleteHost(context.Background(), providerID); err != nil {
		slog.Error("Failed to delete idle host VM", "error", err, "providerID", providerID)
	} else {
		slog.Info("Deleted idle host VM", "providerID", providerID)
	}
}
