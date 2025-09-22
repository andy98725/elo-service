package middleware

import (
	"github.com/labstack/echo"
	m "github.com/labstack/echo/middleware"
)

func AllowCors() echo.MiddlewareFunc {
	return m.CORSWithConfig(m.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowHeaders: []string{echo.HeaderOrigin, echo.HeaderContentType, echo.HeaderAccept},
	})
}
