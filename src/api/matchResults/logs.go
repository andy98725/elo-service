package matchResults

import (
	"context"
	"fmt"
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
	"gorm.io/gorm"
)

// Internal helper that saves the logs of a RUNNING MATCH.
func saveMatchLogs(matchID string) (string, error) {
	match, err := models.GetMatch(matchID)
	if err != nil {
		return "", err
	}

	if match.MachineLogsPort == 0 {
		return "", nil
	}

	resp, err := http.Get(fmt.Sprintf("http://%s:%d/logs", match.MachineIP, match.MachineLogsPort))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	logsKey, err := server.S.AWS.UploadLogs(context.Background(), resp.Body)
	if err != nil {
		return "", err
	}

	return logsKey, nil
}

func GetMatchLogs(ctx echo.Context) error {
	matchID := ctx.Param("matchID")
	userID, err := models.UserIDFromContext(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting user: "+err.Error())
	}

	matchResult, err := models.GetMatchResult(matchID)
	if err == gorm.ErrRecordNotFound {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	} else if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting match result: "+err.Error())
	}

	if canSee, err := models.CanUserSeeMatchResult(userID, matchID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error checking if user can see match result: "+err.Error())
	} else if !canSee {
		return echo.NewHTTPError(http.StatusNotFound, "Match result not found")
	}

	if matchResult.LogsKey == "" {
		return echo.NewHTTPError(http.StatusNotFound, "No logs.")
	}

	logs, err := server.S.AWS.GetLogs(context.Background(), matchResult.LogsKey)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "error getting logs: "+err.Error())
	}

	return ctx.Stream(http.StatusOK, "text/plain", logs)
}
