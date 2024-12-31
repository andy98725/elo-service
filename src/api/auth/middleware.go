package auth

import (
	"database/sql"
	"net/http"

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

		return next(c)
	}
}

func RequireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
	return RequireAuth(func(c echo.Context) error {
		db := c.Get("db").(*sql.DB)
		userID := c.Get("user_id").(string)

		var isAdmin bool

		err := db.QueryRow("SELECT is_admin FROM users WHERE id = $1", userID).Scan(&isAdmin)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "Error checking admin status")
		}

		if !isAdmin {
			return echo.NewHTTPError(http.StatusForbidden, "Admin access required")
		}

		return next(c)
	})
}
