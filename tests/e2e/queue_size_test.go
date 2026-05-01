package e2e

import (
	"flag"
	"fmt"
	"net/http"
	"testing"
)

// TestQueueSizeReflectsJoin verifies /match/size sees a player after
// they join the queue and drops back to 0 after they /disconnect.
// Documents the "send /disconnect to leave cleanly" behavior: TCP
// close removes the player only on the next GC sweep (~3 min), but
// /disconnect is synchronous.
//
// go test -v -run TestQueueSizeReflectsJoin ./tests/e2e -args -url https://elomm.net
func TestQueueSizeReflectsJoin(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "qsizeguest")

	AssertQueueEmpty(t, *baseURL, exampleGameID, token)

	ws := WebsocketConnect(t,
		fmt.Sprintf("%s/match/join?gameID=%s", *baseURL, exampleGameID), token)
	// Read the first frame so we know the JoinQueue path completed
	// before we check size — otherwise the size poll can race the
	// queue insert.
	if _, _, err := ws.ReadMessage(); err != nil {
		t.Fatalf("ws read after join: %v", err)
	}

	resp := DoReq(t, "GET",
		fmt.Sprintf("%s/match/size?gameID=%s", *baseURL, exampleGameID),
		nil, token, http.StatusOK)
	got, _ := resp["players_in_queue"].(float64)
	if got != 1 {
		t.Fatalf("after one /match/join: expected players_in_queue=1, got %v", resp)
	}

	// Send /disconnect to leave the queue synchronously.
	if err := ws.WriteMessage(1, []byte("/disconnect")); err != nil {
		t.Fatalf("write /disconnect: %v", err)
	}
	// Drain until the connection closes.
	for {
		if _, _, err := ws.ReadMessage(); err != nil {
			break
		}
	}
	ws.Close()

	AssertQueueEmpty(t, *baseURL, exampleGameID, token)
}

// TestQueueSizeRejectsMissingGameID verifies the input contract: no
// gameID query param → 400. A common client bug is to call
// /match/size without a gameID; the response should be a clear error.
//
// go test -v -run TestQueueSizeRejectsMissingGameID ./tests/e2e -args -url https://elomm.net
func TestQueueSizeRejectsMissingGameID(t *testing.T) {
	flag.Parse()
	token, _ := LoginGuest(t, *baseURL, "qsizenogame")
	status, body := DoReqStatus(t, "GET",
		fmt.Sprintf("%s/match/size", *baseURL), nil, token)
	if status != http.StatusBadRequest {
		t.Fatalf("missing gameID: expected 400, got %d (%s)", status, body)
	}
}
