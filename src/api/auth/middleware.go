package auth

import (
	"log/slog"
	"net/http"

	"github.com/andy98725/elo-service/src/models"
	"github.com/labstack/echo"
)

func RequireUserOrGuestAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tokenString := c.Request().Header.Get("Authorization")
		if tokenString == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Remove "Bearer " prefix if present
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		if claims, err := ValidateUserToken(tokenString); err == nil {
			user, err := models.GetById(claims.UserID)
			if err != nil {
				slog.Error("Error getting user", "error", err)
				return echo.NewHTTPError(http.StatusInternalServerError, "Error getting user: "+err.Error())
			}

			c.Set("user", user)
			c.Set("id", claims.UserID)
			return next(c)
		}

		if claims, err := ValidateGuestToken(tokenString); err == nil {
			c.Set("guest", models.Guest{
				ID:          claims.ID,
				DisplayName: claims.DisplayName,
			})
			c.Set("id", claims.ID)
			return next(c)
		}

		return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
	}
}

func RequireUserAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tokenString := c.Request().Header.Get("Authorization")
		if tokenString == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Remove "Bearer " prefix if present
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		claims, err := ValidateUserToken(tokenString)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		user, err := models.GetById(claims.UserID)
		if err != nil {
			slog.Error("Error getting user", "error", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "Error getting user: "+err.Error())
		}

		// Add claims to context
		c.Set("user", user)
		c.Set("id", claims.UserID)

		return next(c)
	}
}
func RequireGuestAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		tokenString := c.Request().Header.Get("Authorization")
		if tokenString == "" {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Remove "Bearer " prefix if present
		if len(tokenString) > 7 && tokenString[:7] == "Bearer " {
			tokenString = tokenString[7:]
		}

		claims, err := ValidateGuestToken(tokenString)
		if err != nil {
			return echo.NewHTTPError(http.StatusUnauthorized, "Unauthorized")
		}

		// Add claims to context
		c.Set("guest", models.Guest{
			ID:          claims.ID,
			DisplayName: claims.DisplayName,
		})
		c.Set("id", claims.ID)

		return next(c)
	}
}

func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return RequireUserAuth(func(c echo.Context) error {
		user := c.Get("user").(*models.User)

		if !user.IsAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
		}

		return next(c)
	})
}
