package matchResults

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/andy98725/elo-service/src/server"
	"github.com/labstack/echo"
)

type ReportResultsRequest struct {
	TokenID  string `json:"token_id"`
	WinnerID string `json:"winner_id"`
	Reason   string `json:"reason"`
}

func ReportResults(c echo.Context) error {
	req := new(ReportResultsRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	match, err := models.GetMatchByTokenID(req.TokenID)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Match not found")
	}

	status, err := EndMatch(c.Request().Context(), match, req.WinnerID, req.Reason)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to end match")
	}
	return c.JSON(http.StatusOK, echo.Map{"message": status})
}

func EndMatch(ctx context.Context, match *models.Match, winnerID string, reason string) (string, error) {
	status := ""
	if isUnderway, err := models.IsMatchUnderway(match.ID); err != nil {
		return "", err
	} else if !isUnderway {
		return "", errors.New("match is not underway")
	}

	logsKey, err := saveMatchLogs(match.ID)
	if err != nil {
		slog.Warn("Failed to save match logs", "error", err, "matchID", match.ID)
		logsKey = ""
		status = "Failed to save match logs"
	}
	if _, err := models.MatchEnded(match.ID, winnerID, reason, logsKey); err != nil {
		slog.Error("Failed to report match result", "error", err, "matchID", match.ID)
		return "", err
	}
	if err := server.S.Machines.DeleteServer(ctx, match.MachineName); err != nil {
		slog.Error("Failed to stop machine. Machine will continue to run.", "error", err, "matchID", match.ID, "MachineName", match.MachineName)
		return "", err
	}

	if status == "" {
		status = "Thank you!"
	}
	return status, nil
}
