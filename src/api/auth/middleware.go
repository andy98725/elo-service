package auth

import (
	"log/slog"
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

func RequireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tokenString := c.Request().Header.Get("Authorization")
		if tokenString == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Remove "Bearer " prefix if present
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		claims, err := ValidateToken(tokenString)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Add claims to context
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		user, err := models.GetById(claims.UserID)
		if err != nil {
			slog.Error("Error getting user", "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Error getting user: "+err.Error())
		}
		c.Set("user", user)

		return next(c)
	}
}

func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return RequireAuth(func(c echo.Context) error {
		user := c.Get("user").(*models.User)

		if !user.IsAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
		}

		return next(c)
	})
}
