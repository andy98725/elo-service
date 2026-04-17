package util

import (
	"context"
	"net/http"
	"time"
)

// WaitUntilServerReady polls the given URL every second until it returns HTTP 200,
// the context is cancelled, or the shutdown channel is closed.
func WaitUntilServerReady(ctx context.Context, url string, shutdown <-chan struct{}) (bool, error) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			resp, err := http.Get(url)
			if err != nil {
				continue
			}
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		case <-shutdown:
			return false, nil
		}
	}
}
