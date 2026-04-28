package playerData

import (
	"io"
	"net/http"

	"github.com/labstack/echo"
)

// readBoundedBody reads the request body up to maxBytes+1, so we can
// distinguish "exactly at the cap" from "over the cap". Returns 413 if the
// body would exceed maxBytes. Empty bodies return ([], nil) — the value
// validator catches the empty case with a clearer 400.
func readBoundedBody(c echo.Context, maxBytes int) ([]byte, error) {
	r := http.MaxBytesReader(c.Response().Writer, c.Request().Body, int64(maxBytes)+1)
	defer r.Close()
	buf, err := io.ReadAll(r)
	if err != nil {
		// MaxBytesReader returns *http.MaxBytesError on overflow; any
		// read error here is treated as oversize for simplicity (the
		// 413 error message is accurate either way).
		return nil, echo.NewHTTPError(http.StatusRequestEntityTooLarge, "value exceeds 64KB")
	}
	if len(buf) > maxBytes {
		return nil, echo.NewHTTPError(http.StatusRequestEntityTooLarge, "value exceeds 64KB")
	}
	return buf, nil
}
