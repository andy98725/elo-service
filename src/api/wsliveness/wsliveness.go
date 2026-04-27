// Package wsliveness wires server-driven WebSocket keepalive onto the
// matchmaking and lobby connections.
//
// Background: the matchmaking and lobby flows hold a WebSocket open for as
// long as the player is searching/lobbying. Without an explicit liveness
// check, half-open TCP connections (mobile NAT drops, sleeping laptops,
// silent network failures) go undetected for minutes — the server keeps
// refreshing the player's queue TTL, the player gets paired into a real
// match they never connect to, and the host slot is wasted until match GC
// fires after MATCH_MAX_DURATION.
//
// Mechanism: the server emits Ping frames on PingInterval. RFC 6455 clients
// (browsers, gorilla/websocket, most native libraries) reply with Pong
// automatically. Pong receipt resets a per-connection "last seen"
// timestamp; if PongGrace elapses with nothing received we consider the
// peer dead.
//
// Soft vs hard mode: server.S.Config.WSLivenessDisconnectEnabled controls
// what happens on a timeout.
//   - soft (false, current default): a watchdog goroutine logs a warning
//     once per stale streak. The connection stays open. Lets us roll the
//     server change out before all clients are guaranteed to send pongs.
//   - hard (true): the underlying read deadline is set on the connection,
//     so the next ReadMessage returns an i/o timeout error. Callers
//     observing that error tear the handler down.
package wsliveness

import (
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/andy98725/elo-service/src/server"
	"github.com/gorilla/websocket"
)

const (
	// PingInterval is how often the server emits a Ping frame.
	PingInterval = 30 * time.Second
	// PongGrace is the maximum tolerable gap between reads (any frame, but
	// in practice Pong) before the peer is considered dead. Sized to allow
	// two missed pings plus slack for jittery mobile networks.
	PongGrace = 75 * time.Second
	// writeControlTimeout caps how long a Ping write can block on a backed-
	// up TCP send buffer before we give up and try again next tick.
	writeControlTimeout = 10 * time.Second
)

// Install attaches a Pong handler + ping ticker to conn. The returned
// channel must be closed by the caller when the WS handler exits, to stop
// the background goroutines.
//
// label is used purely for log lines (e.g. "match/join", "lobby/host") so
// soft-mode warnings can be attributed to the right route.
//
// In hard mode this also sets a read deadline that the caller's read pump
// will observe; callers must have a goroutine calling conn.ReadMessage for
// pongs to be processed at all (gorilla dispatches the Pong handler from
// inside ReadMessage).
func Install(conn *websocket.Conn, label, peerID string) chan struct{} {
	stop := make(chan struct{})
	hard := server.S.Config.WSLivenessDisconnectEnabled

	var lastSeenNanos atomic.Int64
	lastSeenNanos.Store(time.Now().UnixNano())

	if hard {
		conn.SetReadDeadline(time.Now().Add(PongGrace))
	}
	conn.SetPongHandler(func(string) error {
		lastSeenNanos.Store(time.Now().UnixNano())
		if hard {
			conn.SetReadDeadline(time.Now().Add(PongGrace))
		}
		return nil
	})

	go pingLoop(conn, stop)
	if !hard {
		go softWatchdog(&lastSeenNanos, stop, label, peerID)
	}

	return stop
}

func pingLoop(conn *websocket.Conn, stop chan struct{}) {
	t := time.NewTicker(PingInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			deadline := time.Now().Add(writeControlTimeout)
			if err := conn.WriteControl(websocket.PingMessage, nil, deadline); err != nil {
				return
			}
		case <-stop:
			return
		}
	}
}

func softWatchdog(lastSeenNanos *atomic.Int64, stop chan struct{}, label, peerID string) {
	t := time.NewTicker(PongGrace / 2)
	defer t.Stop()
	warned := false
	for {
		select {
		case <-t.C:
			last := time.Unix(0, lastSeenNanos.Load())
			gap := time.Since(last)
			if gap > PongGrace {
				if !warned {
					slog.Warn("WS client missed pong (soft mode; not disconnecting)",
						"label", label, "peerID", peerID, "gap", gap.Round(time.Second))
					warned = true
				}
			} else if warned {
				warned = false
			}
		case <-stop:
			return
		}
	}
}
