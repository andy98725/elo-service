package matchResults

import (
	"context"
	"net/http"

	"github.com/andy98725/elo-service/src/external/hetzner"
	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// saveMatchLogs fetches container logs from the host agent and uploads them to S3.
// Returns the S3 key, or empty string if logs could not be saved (non-fatal).
func saveMatchLogs(ctx context.Context, si *models.ServerInstance) (string, error) {
	logs, err := hetzner.GetContainerLogs(ctx,
		si.MachineHost.PublicIP, si.MachineHost.AgentPort, si.MachineHost.AgentToken,
		si.ContainerID)
	if err != nil {
		return "", err
	}

	return server.S.AWS.UploadLogs(ctx, logs)
}

func GetMatchLogs(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	id, ok := ctx.Get("id").(string)
	if !ok || id == "" {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting requester id")
	}

	matchResult, err := models.GetMatchResult(matchID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting match result: "+err.Error())
	}

	if canSee, err := models.CanUserSeeMatchResult(id, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	}

	if !matchResult.Game.PublicMatchLogs {
		if isAdmin, err := models.IsUserMatchResultAdmin(id, matchID); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user is admin: "+err.Error())
		} else if !isAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "Match logs are not public.")
		}
	}

	if matchResult.LogsKey == "" {
		return echo.NewHTTPError(http.StatusNotFound, "No logs.")
	}

	logs, err := server.S.AWS.GetLogs(context.Background(), matchResult.LogsKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting logs: "+err.Error())
	}
	defer logs.Close()

	return ctx.Stream(http.StatusOK, "text/plain", logs)
}
