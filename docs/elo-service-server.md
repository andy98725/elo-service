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

The container is invoked with:

```
<your-binary> -token <match-token> <connectToken1> <connectToken2> [<connectToken3> …]
```

- `-token <match-token>` — opaque per-match secret used to authenticate game-server calls back to elo-service (`/result/report`, `/match/artifact`, server-authored `/games/.../data/.../...`). It is the bearer credential for those routes.
- The remaining positional args are **connect tokens** — one per expected player. Each is the credential a single client presents on join. Each incoming connection's token must match an entry in this list.

A connect token is a per-(match, player) join credential, not a stable player identity. Its value currently equals the player's ID:
  - Registered users: UUID strings (e.g., `7a8b9c10-…`).
  - Guests: prefixed with `g_` (e.g., `g_7a8b9c10-…`).

A planned change swaps the value for an opaque per-(match, player) generated secret. The argv shape and the validation rule ("token must be in the expected set") stay the same — only the values become non-identifying.

The number of connect tokens equals the game's `lobby_size`. They arrive in no particular order.

The container must parse argv before doing anything else and fail loudly if either `-token` or the connect-token list is missing — those inputs are required, and absence indicates a misconfigured invocation that has no recoverable path.

```go
// Reference: example-game-server/main.go, func main()
var matchToken string
flag.StringVar(&matchToken, "token", "", "Match auth token (required)")
flag.Parse()
connectTokens := flag.Args()
if matchToken == "" || len(connectTokens) == 0 { log.Fatal("…") }
```

### 2. Logging

Write everything to **stdout and/or stderr**. After the match ends, the matchmaker fetches `docker logs <container>` from the host agent and uploads it to S3 as the official match log. Logs are downloadable via `/results/{matchID}/logs` but the route is **restricted to the game's owner and site admins** — match participants and other users cannot read them. The legacy `Game.public_match_logs` flag is no longer consulted.

Do **not** depend on a sidecar — the production flow uses Docker logs directly. There is a `game-server-sidecar` image in this repo that tails `/shared/server.log`, but it is not currently composed alongside the game-server container by the host agent; logs come from stdout/stderr.

### 2b. Spectator stream (optional, separate from logs)

When the game has `spectate_enabled=true`, your container can opt into a **near-live spectator broadcast**. This is a **different pipe from logs** — separate file, separate retention, separate visibility — for game-state snapshots (board positions, score, replay frames, etc.) that strangers can tail.

The contract is dead simple:

- Write to **`/shared/spectate.stream`** inside your container. The agent host-mounts this directory; your bytes appear on the host filesystem instantly.
- **Append-only.** Don't seek, don't truncate, don't rotate. The matchmaker tracks a byte offset; rewriting earlier ranges is unspecified.
- **Format is yours.** The matchmaker passes opaque bytes through; the spectator UI knows your game and decodes them. Common shapes: NDJSON of state diffs, length-prefixed binary frames, plain text — pick whatever your client deserializes cheaply.
- **Cadence is yours.** Write whenever you have something to broadcast. The matchmaker polls roughly every 1s, batches the new bytes into a chunk, and uploads to S3.

What you don't have to do:

- No HTTP server. No second port. No auth code (separate from logs and result reporting).
- No need to handle reads — spectator clients pull from the matchmaker, not from you.
- Nothing to do when the match ends. The matchmaker stops polling, finalizes the manifest, and the bytes get moved to a replay archive prefix automatically. Replays are retained indefinitely.

Latency the spectator sees is roughly **chunk_interval + S3 RTT + spectator-client poll** — typically **5–15 seconds**. Don't promise real-time spectating to your players; this is a delayed broadcast.

Opt out at any time by simply not writing to the file. Per-match opt-out is also available to lobby hosts (the `?spectate=false` parameter on `/lobby/host`).

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

There is **no Authorization header** on this endpoint — the per-match `token_id` *is* the credential. A successful report ends the match and writes the `MatchResult`; the result itself is immutable, so a second report returns `409 Conflict — match already ended`. It's safe to retry on network failure, but treat any 2xx as terminal.

> **Post-result cooldown.** After a successful 2xx, your container and `token_id` stay alive for a grace window (default **5 minutes**, controlled by `MATCH_COOLDOWN_DURATION` on the matchmaker). During the window you can still call `POST /match/artifact` and the server-authored `/games/{gameID}/data/{playerID}/...` endpoints — useful when artifact uploads or final stat writes happen async after you decide the result. Calling `/result/report` again is rejected. After the window, the worker stops your container and invalidates the token; further calls get `401`. Don't assume any specific moment within the window for shutdown — exit cleanly whenever your post-match work is done.

> **Where does the URL come from?** The matchmaker does **not** inject any environment variables into your container, so the reporting URL has to come from somewhere you control. Two common patterns:
>
> 1. **Hard-code it** in your source. Simplest; fine for production-only servers. Use `https://elomm.net/result/report`.
> 2. **Bake it via Dockerfile `ENV`** — useful if you want to point at a local elo-service during development. The reference image uses this (currently pinned to the Fly-assigned public hostname, which routes to the same service):
>     ```dockerfile
>     ARG WEBSITE_URL=https://elo-service.fly.dev/result/report
>     ENV WEBSITE_URL=$WEBSITE_URL
>     ```
>    Then your code reads `os.Getenv("WEBSITE_URL")`. Override at build time with `--build-arg WEBSITE_URL=...`. `https://elomm.net/result/report` and `https://elo-service.fly.dev/result/report` both reach the same backend.

After a successful POST, the host agent will stop and remove your container after the cooldown window described above (default 5 min). Exit cleanly whenever your post-match work is done; the platform will catch up.

### 4b. Match artifacts (optional, separate from spectator stream)

You can attach **named artifacts** to a match — replay files, preview images, highlight reels, AI-training snapshots, anything else worth keeping post-match. Artifacts persist on the `MatchResult` so they're discoverable via `/user/artifacts` and `/matches/{matchID}/artifacts`. The matchmaker treats the bytes as opaque.

```http
POST https://elomm.net/match/artifact?name=<artifact-name>
Authorization: Bearer <the same token_id you'll use for /result/report>
Content-Type: <whatever describes the bytes — image/png, application/octet-stream, etc.>

<binary body>
```

Rules:

- **Auth** is your match's `token_id` in the `Authorization: Bearer …` header — same credential as `/result/report`. Valid while the match is underway **and** through the post-result cooldown window (see "Post-result cooldown" above). After the cooldown window expires the call returns `403 match is not underway`.
- **Name** must match `[a-zA-Z0-9._-]{1,64}`. Re-uploading the same name overwrites. Up to **10 distinct names** per match.
- **Body cap**: 1 MiB. `413 Request Entity Too Large` for anything bigger. Use the spectator stream for high-bandwidth data.
- **Content-Type** is preserved exactly and returned as the response Content-Type when clients download — set it correctly so `image/png` thumbnails render in browsers.

Conventional names that platform-generic UIs may render specially (recommended, not enforced):
- `preview` — small image (PNG/JPEG) for match-history thumbnails
- `replay` — game-defined replay file the client can re-render

You can upload mid-match (post-state-snapshot, after each round) or all-at-once just before `/result/report` — whichever fits your game. The artifact is bound to the match by the auth token, so timing within the match window doesn't matter.

> **Why isn't this part of `/result/report`?** Multipart on the result-report endpoint complicates a previously simple JSON contract. Separate calls also let you upload artifacts incrementally during the match without waiting for game-end.

---

## How players connect to you

The matchmaker hands each client a payload like:

```json
{ "status":        "match_found",
  "server_host":   "host-a4f9b2d8-1234-5678-90ab-cdef01234567.gs.elomm.net",   // OR raw IPv4
  "server_ports":  [7042, 7043],
  "match_id":      "<uuid>",
  "connect_token": "<opaque string>" }
```

The hostname format is `host-<machine-host-uuid>.gs.elomm.net` — the full host UUID, not a short slug.

The client opens a connection to `<server_host>:<server_ports[i]>`. They will connect using:

- `ws://` or `wss://` for WebSocket
- `http://` or `https://` for HTTP
- raw TCP/UDP for native protocols

**On TLS-enabled hosts**, browser clients use `wss://` / `https://` and Caddy terminates TLS before traffic reaches you. Your server sees plain HTTP/TCP from the Docker bridge gateway (typically `172.17.0.1`), not the original client's address — so don't try to use `RemoteAddr` for player identification or geo-IP. **You do not need to handle TLS in your code.**

### Player identification

The `match_found` payload carries a `connect_token` per player. Each client presents its token on join; the game server admits the connection if the value matches one of the connect tokens received in argv.

The canonical wire format (used by `example-game-server`):

- HTTP: `POST /join` with the connect token as the request body (plain text).
- TCP: First line is the connect token, terminated by `\n`.

Validation responses:

| Condition | Response |
|---|---|
| Connect token is in the expected list and has not joined yet | `200 OK` (HTTP) / `OK: …\n` (TCP) |
| Connect token is empty or malformed | `400 Bad Request` |
| Connect token is not in the expected list | `403 Forbidden` |
| Connect token has already joined | `409 Conflict` |

The match begins once every expected connect token has joined.

### Trust model

In the current rollout, connect tokens equal player IDs — a determined attacker who guesses or steals another player's ID can impersonate them. A planned change swaps the value for an opaque per-(match, player) secret generated by the matchmaker, which raises the bar to "intercept the WebSocket / TLS channel that delivered `match_found`." The argv shape and validation rule are unchanged between phases.

Stronger guarantees are also available out-of-band: a game server can require clients to send their full JWT and verify it against elo-service. The matchmaker provides no helper for this path; no current game uses it.

---

## Per-player game data (achievements, progression, server-authored state)

elo-service includes a per-(game, player) JSON key-value store you can use as a minimal backend — store achievements, level progression, last-known scores, server-issued cosmetic unlocks, anything you want the player's client to be able to read without standing up your own database.

Each (game, player) row has **two independent namespaces** sharing the same key space:

- **Server-authored** — you write these from your container. The player's client can read them but can't modify them.
- **Player-authored** — the client writes these (settings, preferences). You can read them but can't modify them.

The same key can exist in both namespaces with independent values; they don't collide. Use server-authored for anything the player shouldn't be able to forge (achievement unlocks, rating-adjacent state, etc.) and let the client read it via the player-side API documented in `elo-service-client.md`.

### Auth: reuse your match token

All four endpoints below require **the same `-token` value you got in argv**, passed as `Authorization: Bearer <token>`. The matchmaker:

- Resolves the token to a match.
- Confirms the match is underway or within its post-result cooldown window (the row is deleted only after the cooldown sweep — auth fails after that).
- Confirms the URL `gameID` matches the match's game.
- Confirms the URL `playerID` is one of the players in your match.
- Rejects guest player IDs (`g_…`) with `400` — guests are intentionally unsupported on this feature.

So you can only read or write data for players in **your current match**, and only while the match is underway. There is no long-lived per-game credential to manage.

### Read a player's data

```http
GET https://elomm.net/games/{gameID}/data/{playerID}/player
GET https://elomm.net/games/{gameID}/data/{playerID}/server
Authorization: Bearer <your -token value>
```

Both return the same shape:
```json
{ "entries": { "achievements.first_win": {"at":"2026-04-28"}, "level": 12 } }
```

`entries` is a keyed object — values are arbitrary JSON. Empty object `{}` if the player has no entries on that side.

Use the `…/player` endpoint to read what the player wrote (settings, preferences); use the `…/server` endpoint to read what your previous matches wrote (carried-over progression, achievements, etc.).

### Upsert a server-authored entry

```http
PUT https://elomm.net/games/{gameID}/data/{playerID}/{key}
Authorization: Bearer <your -token value>
Content-Type: application/json

<arbitrary JSON value>
```

The request body **is** the value — no envelope. Examples you might write at end-of-match:

- `PUT …/player-uuid/achievements.first_win` body `{"at":"2026-04-28","match_id":"…"}` — grant an achievement.
- `PUT …/player-uuid/level` body `13` — bump player level.
- `PUT …/player-uuid/last_match_score` body `{"score":4200,"placement":1}` — store a stat the client can render.

Replaces any existing server-authored entry at the same key. Does not touch a player-authored entry at the same key.

Response `200`: `{"status":"ok"}`.

### Delete a server-authored entry

```http
DELETE https://elomm.net/games/{gameID}/data/{playerID}/{key}
Authorization: Bearer <your -token value>
```

Response `200` if the entry existed, `404` if it didn't. Server can only delete server-authored entries; the player owns the player-authored side.

### Limits & validation

| Constraint | Limit |
|---|---|
| Key format | `[a-zA-Z0-9._-]{1,128}` — letters, digits, dot, underscore, hyphen, max 128 chars |
| Value size | 64 KB serialized JSON |
| Value format | Must be valid JSON |

Error codes:
- `400` — key doesn't match the regex, body isn't valid JSON, or `playerID` is a guest (`g_…`).
- `401` — token missing or no longer resolves (match has ended).
- `403` — `gameID` in the URL doesn't match the match's game, or `playerID` isn't in your match.
- `404` — DELETE on a key that doesn't exist.
- `413` — value larger than 64 KB.

### Patterns

- **Grant on result** — write achievements / level deltas in the same critical section as your `POST /result/report`. Order doesn't matter; both endpoints take the same `-token`. Writes also work during the post-result cooldown window (see "Post-result cooldown" in §4), so you can defer end-of-match data writes until after the result POST without racing teardown.
- **Carry-over state** — at match start, `GET …/{playerID}/server` for each player to load their persisted level/progression before the game begins. Write back at end-of-match.
- **Read-modify-write races** — full-blob writes; if two of your servers write the same key concurrently, last write wins. For counter-style state that needs atomicity, you'll need to add your own server-side merge logic (or model achievements as separate keys per achievement, which sidesteps the issue).

For the player-side read/write API (settings, etc.), see `elo-service-client.md`.

---

## Registering your game

Before your image can be used, register a `Game` record. This requires a registered (non-guest) user account **with the `can_create_game` flag set**. The flag defaults to `false` for new accounts and is admin-grantable only — if you've just signed up, **this requires admin assistance**. Without it the call returns `403 "user is not allowed to create games"`.

```http
POST /game
Authorization: Bearer <your user token>
Content-Type: application/json

{
  "name":                       "TicTacToe",
  "description":                "2-player tic tac toe",
  "guests_allowed":             true,
  "public_results":             true,
  "public_match_logs":          false,

  "lobby_enabled":              true,
  "lobby_size":                 2,
  "matchmaking_strategy":       "random",
  "matchmaking_machine_name":   "your-dockerhub-user/your-image:latest",
  "matchmaking_machine_ports":  [8080],
  "elo_strategy":               "unranked",
  "metadata_enabled":           false
}
```

The matchmaking fields (the second block) seed an auto-created **primary queue** under the new game — see "Queues" below. The flat-fields shape on `POST /game` and `PUT /game/{id}` is preserved for backwards compatibility; updates apply to the game's primary queue. Multi-queue games address each queue directly via `/game/{id}/queue/{queueID}`.

Field reference:

| Field | Required | Default | Notes |
|---|---|---|---|
| `name` | yes | — | Globally unique. `409` if taken. |
| `description` | no | `""` | User-facing. |
| `guests_allowed` | no | `true` | If `false`, only registered users can queue. |
| `public_results` | no | `true` | If `true`, anyone can see match results for this game; if `false`, only participants and the game owner. |
| `public_match_logs` | no | `false` | **Deprecated** — no longer consulted. Match logs are restricted to the game's owner and site admins regardless of this flag. The field is preserved for now to avoid breaking existing CRUD callers but has no effect. |
| `matchmaking_machine_name` | no | `docker.io/andy98725/example-server:latest` | Primary queue field. Full Docker image ref (`registry/repo:tag`). The host VM must be able to `docker pull` it — public images on Docker Hub work; private registries need credentials configured on the host VM image. **Almost always set this**: omitting it silently falls back to the demo example-server, which is rarely what you want for a real game. |
| `matchmaking_machine_ports` | no | `[]` | Primary queue field. Ports your container listens on. Order is preserved in the client `server_ports` array. |
| `lobby_size` | no | `2` | Primary queue field. Players per match — matchmaker waits for this many before spawning. |
| `lobby_enabled` | no | `true` | Primary queue field. Whether the lobby flow (`/lobby/*`) is allowed for this queue. |
| `matchmaking_strategy` | no | `"random"` | Primary queue field. `"random"` or `"rating"`. Affects who pairs with whom in the queue. |
| `elo_strategy` | no | `"unranked"` | Primary queue field. `"unranked"` (no rating updates) or `"classic"` (Elo). |
| `default_rating` | no | `1000` | Primary queue field. Initial rating assigned the first time a player is rated in this queue. |
| `k_factor` | no | `32` | Primary queue field. Elo K factor (only used when `elo_strategy="classic"`). |
| `metadata_enabled` | no | `false` | Primary queue field. If `true`, the `metadata` query param on `/match/join` segments the queue (e.g., by region or game mode). |

Response `200`: a `GameResp` with the new `id` (UUID) and a `queues` array (one entry: the primary queue). Per-queue config lives entirely under `queues[]` — read it from there.

Update with `PUT /game/{id}` (only the owner). Delete with `DELETE /game/{id}` — cascades to all queues, ratings, and per-game player data.

### Queues

A `Game` is identity + game-wide policy (name, owner, public-results flag). The matchmaking-flavored knobs (image, ports, lobby size, ELO strategy) live on **`GameQueue` records** — one game has 1..N queues. Use cases:

- **Different game modes** under one game: `1v1-ranked`, `2v2-ranked`, `casual`. Each maintains its own ladder (rating rows are keyed by `(player, game_queue)`).
- **Different images** for the same game: a stable build vs. a beta build, with players choosing which queue to enter.
- **Different ELO settings**: a high-K-factor "fast track" queue alongside a low-K-factor "stable" queue.

The "primary" queue is created automatically by `POST /game` from the legacy flat fields. The default queue (used when API callers don't specify a `queueID`) is the **oldest queue** by `created_at` — typically the primary, unless you've deleted it (in which case the next-oldest auto-promotes).

Endpoints:

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST`   | `/game/{gameID}/queue` | owner only | Add a queue. Body matches the matchmaking-fields shape on `POST /game`, plus a `name` (unique within the game). |
| `GET`    | `/game/{gameID}/queue` | public | List all queues for a game in canonical order (oldest first; `[0]` is the default). |
| `GET`    | `/game/{gameID}/queue/{queueID}` | public | Fetch one queue. |
| `PUT`    | `/game/{gameID}/queue/{queueID}` | owner only | Update queue settings (conditional update — only non-zero fields are applied). |
| `DELETE` | `/game/{gameID}/queue/{queueID}` | owner only | Delete a queue. Returns `409` if it's the only remaining queue for the game. Cascades to its ratings. |

The matchmaking, lobby, and rating endpoints all accept an optional `queueID` query param. Omit it and they default to the game's primary queue — existing single-queue clients keep working without code changes.

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
    var matchToken string
    flag.StringVar(&matchToken, "token", "", "match token (required)")
    flag.Parse()
    connectTokens := flag.Args()
    if matchToken == "" || len(connectTokens) == 0 {
        log.Fatal("missing -token or connect tokens")
    }
    log.Printf("starting match: %d expected players", len(connectTokens)) // never log -token

    expected := map[string]bool{}
    for _, t := range connectTokens {
        expected[t] = true
    }

    var mu sync.Mutex
    joined := map[string]bool{}
    done := make(chan struct{})

    http.HandleFunc("/join", func(w http.ResponseWriter, r *http.Request) {
        body, _ := io.ReadAll(r.Body)
        tok := string(body)
        if !expected[tok] {
            http.Error(w, "not expected", http.StatusForbidden)
            return
        }
        mu.Lock()
        defer mu.Unlock()
        if joined[tok] {
            http.Error(w, "already joined", http.StatusConflict)
            return
        }
        joined[tok] = true
        log.Printf("player joined (%d/%d)", len(joined), len(expected))
        if len(joined) == len(expected) {
            close(done)
        }
    })

    go http.ListenAndServe(":8080", nil)

    <-done
    // …run the game…
    // Phase 1: connect tokens equal player IDs, so a token doubles as a
    // valid winner_id. Phase 2 will require an explicit (player_id,
    // connect_token) mapping in argv to keep this honest.
    var winner string
    for tok := range joined {
        winner = tok
        break
    }

    body, _ := json.Marshal(map[string]any{
        "token_id":   matchToken,
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
- **Lifetime.** Your container is killed after the post-result cooldown window elapses (default 5 min after `/result/report`; see `MATCH_COOLDOWN_DURATION`), or by garbage collection if the match runs longer than the absolute timeout (~6 hours; see `MATCH_GC_INTERVAL`). Don't rely on long-lived state inside the container.
- **No persistent storage.** Anything you write to disk is gone when the container dies. Persistent game state (ratings, history) is elo-service's responsibility, not yours — you only report winners.
- **Multiple containers per host.** Up to `HCLOUD_MAX_SLOTS_PER_HOST` containers (default 8) share one VM. Don't assume you have the whole CPU/RAM.

---

## Endpoint cheatsheet

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `POST` | `/result/report` | per-match token in body | Report match outcome |
| `POST` | `/game` | user | Register a new game (creates game + primary queue in one call) |
| `PUT`  | `/game/{id}` | game owner | Update game-level fields; flat queue fields apply to the primary queue |
| `DELETE` | `/game/{id}` | game owner | Delete a game (cascades to queues, ratings, player data) |
| `POST` | `/game/{gameID}/queue` | game owner | Create an additional queue (image, ports, ELO settings, etc.) |
| `GET`  | `/game/{gameID}/queue` | public | List all queues for a game (oldest first; `[0]` is the default) |
| `GET`  | `/game/{gameID}/queue/{queueID}` | public | Fetch one queue |
| `PUT`  | `/game/{gameID}/queue/{queueID}` | game owner | Update a queue's matchmaking config |
| `DELETE` | `/game/{gameID}/queue/{queueID}` | game owner | Delete a queue (refused with `409` if it's the last one) |
| `GET`  | `/results/{matchID}/logs` | user (owner/admin only) | Download container stdout — restricted to the game's owner and site admins |
| `GET`  | `/games/{gameID}/data/{playerID}/player` | match token | Read player-authored entries |
| `GET`  | `/games/{gameID}/data/{playerID}/server` | match token | Read server-authored entries |
| `PUT`  | `/games/{gameID}/data/{playerID}/{key}` | match token | Upsert a server-authored entry |
| `DELETE` | `/games/{gameID}/data/{playerID}/{key}` | match token | Delete a server-authored entry |

Full schemas: `https://elomm.net/swagger/index.html`.
