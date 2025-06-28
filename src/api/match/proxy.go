package match

import (
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/labstack/echo"
)

func ReverseProxyHandler(c echo.Context) error {
	machineID := c.Param("machineID")
	if machineID == "" {
		return c.String(http.StatusBadRequest, "machineID is required")
	}
	port := c.Param("port")
	if port == "" {
		return c.String(http.StatusBadRequest, "port is required")
	}

	// Build the target URL by combining the base with request URI
	targetURL, err := url.Parse(fmt.Sprintf("http://%s:%s", machineID, port))
	if err != nil {
		return c.String(http.StatusBadRequest, "Invalid target URL")
	}

	// Create new request with same method/body/headers
	req, err := http.NewRequest(c.Request().Method, targetURL.String(), c.Request().Body)
	if err != nil {
		return c.String(http.StatusInternalServerError, "Failed to create request")
	}
	req.Header = c.Request().Header

	// Forward the request to the internal machine
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return c.String(http.StatusBadGateway, "Failed to reach backend")
	}
	defer resp.Body.Close()

	// Copy headers and status code
	for k, v := range resp.Header {
		c.Response().Header()[k] = v
	}
	c.Response().WriteHeader(resp.StatusCode)

	// Stream body back
	_, err = io.Copy(c.Response(), resp.Body)
	return err
}
