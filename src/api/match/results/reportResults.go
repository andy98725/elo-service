package results

import (
	"log/slog"
	"net/http"

	"github.com/labstack/echo"
)

type ReportResultsRequest struct {
	TokenID  string `json:"token_id"`
	WinnerID string `json:"winner_id"`
}

func ReportResults(c echo.Context) error {
	req := new(ReportResultsRequest)
	if err := c.Bind(req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid request payload")
	}

	slog.Info("ReportResults", "req", req) //TODO
	return c.JSON(http.StatusOK, echo.Map{"message": "Thank you!"})
}
