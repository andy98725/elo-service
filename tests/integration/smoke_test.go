package integration

import (
	"testing"
)

func TestHealthCheck(t *testing.T) {
	h := NewHarness(t)
	resp := DoReq(t, "GET", h.BaseURL()+"/health", nil, "", 200)
	if resp["status"] != "healthy!" {
		t.Fatalf("expected healthy, got %v", resp["status"])
	}
}
