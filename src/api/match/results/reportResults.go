package results

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

	err = EndMatch(c.Request().Context(), match, req.WinnerID, req.Reason)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to end match")
	}
	return c.JSON(http.StatusOK, echo.Map{"message": "Thank you!"})
}

func EndMatch(ctx context.Context, match *models.Match, winnerID string, reason string) error {

	if deleted, err := server.S.Redis.RemoveMatchUnderway(ctx, match.ID); err != nil {
		slog.Error("Failed to remove match underway", "error", err, "matchID", match.ID)
		return err
	} else if !deleted {
		slog.Warn("Match not underway", "matchID", match.ID)
		return errors.New("match not underway")
	}
	if _, err := models.MatchEnded(match.ID, winnerID, reason); err != nil {
		slog.Error("Failed to report match result", "error", err, "matchID", match.ID)
		return err
	}
	if err := server.S.Redis.FreePorts(ctx, match.MachineName); err != nil {
		slog.Error("Failed to free ports", "error", err, "matchID", match.ID)
		return err
	}
	if err := server.StopMachine(match.MachineName); err != nil {
		slog.Error("Failed to stop machine", "error", err, "matchID", match.ID)
		return err
	}
	return nil
}
