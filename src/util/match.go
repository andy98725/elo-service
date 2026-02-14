package util

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

func WaitUntilServerReady(ctx context.Context, ip string, port int64, shutdown <-chan struct{}) (bool, error) {
	serverCheck := time.NewTicker(1 * time.Second)
	defer serverCheck.Stop()

	for {
		select {
		case <-serverCheck.C:
			resp, err := http.Get(fmt.Sprintf("http://%s:%d/health", ip, port))
			if err != nil {
				continue
			}
			if err := resp.Body.Close(); err != nil {
				return false, err
			}
			if resp.StatusCode == 200 {
				return true, nil
			}
		case <-ctx.Done():
			return false, ctx.Err()
		case <-shutdown:
			return false, nil
		}

	}
}
