# elo-service: game-server integration guide

> **Drop this file into your game-server repo.** It's a self-contained reference for AI coding agents (and humans) building a Docker image that elo-service spawns when a match is created. For the client-side counterpart, see `elo-service-client.md`.

To pull a fresh copy:

```bash
curl -O https://raw.githubusercontent.com/andy98725/elo-service/main/docs/elo-service-server.md
```

A "game server" here is a Docker image that elo-service runs once per match. The matchmaker tells your container which players to expect, your container accepts their connections, runs the game, and POSTs the result back. The container is destroyed after the match ends.

---

## The container contract (read this first)

Everything below derives from these four facts:

1. **You receive your config via command-line argv**, not env vars.
2. **You write logs to stdout/stderr** — the matchmaker captures them via the Docker logs API.
3. **You expose ports** declared by the game's registration (any TCP port is fronted by Caddy with TLS on TLS-enabled hosts; UDP is direct).
4. **You report the result** by `POST`ing to `https://elomm.net/result/report` with the per-match token from your argv.

### 1. Argv contract

Your container is invoked with:

```
<your-binary> -token <token> <playerID1> <playerID2> [<playerID3> …]
```

- `-token <token>` — opaque per-match secret. Treat as a bearer credential. **Don't log it, don't expose it to clients.** This is the only thing that authenticates your result POST back to elo-service.
- The remaining positional args are **player IDs**, one per expected player. They are the same IDs the clients themselves know:
  - Registered users: UUID strings (e.g., `7a8b9c10-…`).
  - Guests: prefixed with `g_` (e.g., `g_7a8b9c10-…`).

The number of player IDs equals the game's `lobby_size`. They arrive in no particular order — don't depend on it.

You **must** parse argv before doing anything else and fail loudly if either `-token` or the player ID list is missing.

```go
// Reference: example-game-server/main.go:250-278
flag.StringVar(&tokenID, "token", "", "Token ID (required)")
flag.Parse()
playerIDs := flag.Args()
if tokenID == "" || len(playerIDs) == 0 { log.Fatal("…") }
```

### 2. Logging

Write everything to **stdout and/or stderr**. After the match ends, the matchmaker fetches `docker logs <container>` from the host agent and uploads it to S3 as the official match log (downloadable by clients via `/results/{matchID}/logs` if the game has `public_match_logs=true`).

Do **not** depend on a sidecar — the production flow uses Docker logs directly. There is a `game-server-sidecar` image in this repo that tails `/shared/server.log`, but it is not currently composed alongside the game-server container by the host agent; logs come from stdout/stderr.

### 3. Ports

When you register your game (see below), you declare a list of ports your container exposes — e.g., `[8080, 8081]` for an HTTP+TCP setup. Each match, the host agent:

- Allocates that many free host ports from the pool (default 7000–9000).
- Binds them to the container's exposed ports, in order.
- TCP ports are bound through Caddy with an offset on TLS-enabled hosts (so Caddy can listen on the public port and reverse-proxy with TLS). **Inside your container this is invisible** — you bind `0.0.0.0:<your declared port>` like normal.
- UDP ports are bound directly to the public port (no TLS — Caddy doesn't speak UDP).

The order matters: `server_ports[i]` in the client-facing `match_found` payload corresponds to your declared port `i`. If you declare `[8080, 8081]` and the agent allocates `[7042, 7043]`, then `8080 → 7042` and `8081 → 7043`.

### 4. Result reporting

When the match ends, POST to:

```http
POST https://elomm.net/result/report
Content-Type: application/json

{
  "token_id":   "<the -token value from your argv>",
  "winner_ids": ["<playerID>", …],
  "reason":     "completed"
}
```

- `token_id` is **required**; the server looks up the match by this token. An unrecognized token returns `404 Match not found`.
- `winner_ids` is a list. For single-winner games you can use the legacy `winner_id` (string) field instead — the server normalizes it to a one-element list. An empty array is allowed (draw / abort).
- `reason` is a free-form string; convention is `"completed"` for normal endings, `"timeout"` if you ended early, anything else is fine for your own bookkeeping.

There is **no Authorization header** on this endpoint — the per-match `token_id` *is* the credential. A successful report ends the match; subsequent reports on the same token return `500 Failed to end match`. It's safe to retry on network failure, but treat any 2xx as terminal.

> **Where does the URL come from?** The matchmaker does **not** inject any environment variables into your container, so `https://elomm.net/result/report` has to come from somewhere you control. Two common patterns:
>
> 1. **Hard-code it** in your source. Simplest; fine for production-only servers.
> 2. **Bake it via Dockerfile `ENV`** — useful if you want to point at a local elo-service during development. The reference image uses this:
>     ```dockerfile
>     ARG WEBSITE_URL=https://elomm.net/result/report
>     ENV WEBSITE_URL=$WEBSITE_URL
>     ```
>    Then your code reads `os.Getenv("WEBSITE_URL")`. Override at build time with `--build-arg WEBSITE_URL=...`.

After a successful POST, the host agent will stop and remove your container shortly. Exit cleanly after reporting.

---

## How players connect to you

The matchmaker hands clients a payload like:

```json
{ "status": "match_found",
  "server_host":  "host-abc123.gs.elomm.net",   // OR raw IPv4
  "server_ports": [7042, 7043],
  "match_id":     "<uuid>" }
```

The client opens a connection to `<server_host>:<server_ports[i]>`. They will connect using:

- `ws://` or `wss://` for WebSocket
- `http://` or `https://` for HTTP
- raw TCP/UDP for native protocols

**On TLS-enabled hosts**, browser clients use `wss://` / `https://` and Caddy terminates TLS before traffic reaches you. Your server still sees plain HTTP/TCP from `127.0.0.1`. **You do not need to handle TLS in your code.**

### Player identification

The `match_found` payload **does not include any auth code or signed claim**. Players announce themselves by sending their **player ID** (the same string you got in argv) when they connect. Your job is to validate it.

The canonical pattern (used by `example-game-server`):

- HTTP: `POST /join` with the player ID as the request body (plain text).
- TCP: First line is the player ID, terminated by `\n`.

Your validation rules:

| Condition | Response |
|---|---|
| Player ID is in your expected list and hasn't joined yet | `200 OK` (HTTP) / `OK: …\n` (TCP) |
| Player ID is empty or malformed | `400 Bad Request` |
| Player ID is **not** in your expected list | `403 Forbidden` |
| Player ID has already joined | `409 Conflict` |

Once all expected players have joined, run the game.

### Trust model

The player ID is **not cryptographically signed** at connect time. A determined attacker who guesses or steals another player's ID could impersonate them. For the current games this is treated as acceptable (IDs are UUIDs, single-match tokens). If your game needs stronger auth, you can require the client to also send its full JWT and verify it via the elo-service public key — but no current game does this and the matchmaker doesn't help you with it.

---

## Registering your game

Before your image can be used, register a `Game` record. This requires a non-guest user account.

```http
POST /game
Authorization: Bearer <your user token>
Content-Type: application/json

{
  "name":                       "TicTacToe",
  "description":                "2-player tic tac toe",
  "guests_allowed":             true,
  "lobby_enabled":              true,
  "lobby_size":                 2,
  "matchmaking_strategy":       "random",
  "matchmaking_machine_name":   "your-dockerhub-user/your-image:latest",
  "matchmaking_machine_ports":  [8080],
  "elo_strategy":               "unranked",
  "metadata_enabled":           false
}
```

Field reference:

| Field | Required | Default | Notes |
|---|---|---|---|
| `name` | yes | — | Globally unique. `409` if taken. |
| `description` | no | `""` | User-facing. |
| `matchmaking_machine_name` | yes | — | Full Docker image ref (`registry/repo:tag`). The host VM must be able to `docker pull` it — public images on Docker Hub work; private registries need credentials configured on the host VM image. |
| `matchmaking_machine_ports` | no | `[]` | The ports your container listens on. Order is preserved in the client `server_ports` array. |
| `lobby_size` | no | `2` | Players per match. Matchmaker waits for this many before spawning. |
| `lobby_enabled` | no | `true` | Whether the lobby flow (`/lobby/*`) is allowed for this game. |
| `guests_allowed` | no | `true` | If `false`, only registered users can queue. |
| `matchmaking_strategy` | no | `"random"` | `"random"` or `"rating"`. Affects who pairs with whom in the queue. |
| `elo_strategy` | no | `"unranked"` | `"unranked"` (no rating updates) or `"classic"` (Elo). |
| `metadata_enabled` | no | `false` | If `true`, the `metadata` query param on `/match/join` segments the queue (e.g., by region or game mode). |
| `public_results` | no | `false` | If `true`, anyone can see match results for this game. |
| `public_match_logs` | no | `false` | If `true`, anyone can download container logs. |

Response `200`: a `GameResp` with the new `id` (UUID). That UUID is the `gameID` your clients pass to `/match/join`.

Update with `PUT /game/{id}` (only the owner). Delete with `DELETE /game/{id}`.

---

## Minimal example (Go)

A complete reference implementation lives in `example-game-server/` of the elo-service repo (image: `andy98725/example-server:latest`). The skeleton:

```go
package main

import (
    "bytes"
    "encoding/json"
    "flag"
    "io"
    "log"
    "net/http"
    "sync"
)

const reportURL = "https://elomm.net/result/report"

func main() {
    var token string
    flag.StringVar(&token, "token", "", "match token (required)")
    flag.Parse()
    expected := flag.Args()
    if token == "" || len(expected) == 0 {
        log.Fatal("missing -token or player IDs")
    }
    log.Printf("starting match: players=%v", expected) // never log the token

    expectedSet := map[string]bool{}
    for _, p := range expected {
        expectedSet[p] = true
    }

    var mu sync.Mutex
    joined := map[string]bool{}
    done := make(chan struct{})

    http.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        id := string(body)
        if !expectedSet[id] {
            http.Error(w, "not expected", http.StatusForbidden)
            return
        }
        mu.Lock()
        defer mu.Unlock()
        if joined[id] {
            http.Error(w, "already joined", http.StatusConflict)
            return
        }
        joined[id] = true
        log.Printf("player joined: %s (%d/%d)", id, len(joined), len(expected))
        if len(joined) == len(expected) {
            close(done)
        }
    })

    go http.ListenAndServe(":8080", nil)

    <-done
    // …run the game…
    winner := expected[0] // pick the real winner

    body, _ := json.Marshal(map[string]any{
        "token_id":   token,
        "winner_ids": []string{winner},
        "reason":     "completed",
    })
    resp, err := http.Post(reportURL, "application/json", bytes.NewBuffer(body))
    if err != nil {
        log.Fatalf("report failed: %v", err)
    }
    defer resp.Body.Close()
    log.Printf("reported result: %s", resp.Status)
}
```

Dockerfile is unremarkable — `FROM golang:alpine`, build, `EXPOSE 8080`, `ENTRYPOINT ["/app/server"]`.

> **Don't leak the token.** The reference `example-game-server` returns `token_id` in its `/health` payload, which is convenient for debugging but means anyone who can reach the container can submit a fake match result. Real game servers should keep the token in process memory only — never put it in HTTP responses, headers, or logs.

---

## Health check

The host agent exposes `GET http://<host>:<agentPort>/containers/<id>/health` to the matchmaker, which polls it before sending `match_found` to clients. The check is **container-level**, not application-level: it returns `200` if the Docker container is running, `503` otherwise.

You don't need to expose a `/health` endpoint of your own (though `example-game-server` does, for debugging). Just don't crash on startup. If your container exits before all players join, the matchmaker will give up and clients will see `{"status": "error", "error": "server not ready"}`.

---

## Operational notes

- **Cold starts.** A fresh host VM takes ~30–60 s to provision (Hetzner boot + Docker pull). Once a host is warm, container start is a few seconds. The service maintains a small warm pool (1 slot in production) to absorb the first match's cold start.
- **Lifetime.** Your container is killed after you POST the result, or by garbage collection if the match runs longer than the configured timeout (~minutes; see `MATCH_GC_INTERVAL`). Don't rely on long-lived state inside the container.
- **No persistent storage.** Anything you write to disk is gone when the container dies. Persistent game state (ratings, history) is elo-service's responsibility, not yours — you only report winners.
- **Multiple containers per host.** Up to `HCLOUD_MAX_SLOTS_PER_HOST` containers (default 8) share one VM. Don't assume you have the whole CPU/RAM.

---

## Endpoint cheatsheet

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/result/report` | per-match token in body | Report match outcome |
| `POST` | `/game` | user | Register a new game |
| `PUT`  | `/game/{id}` | game owner | Update game config |
| `DELETE` | `/game/{id}` | game owner | Delete a game |
| `GET`  | `/results/{matchID}/logs` | user/guest | Download container stdout (if public) |

Full schemas: `https://elomm.net/swagger/index.html`.
