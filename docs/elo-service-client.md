# elo-service: client integration guide

> **Drop this file into your game-client repo.** It's a self-contained reference for AI coding agents (and humans) building a client that authenticates against elo-service, joins matchmaking, and connects to the spawned game server. For the server-side counterpart, see `elo-service-server.md`.

To pull a fresh copy:

```bash
curl -O https://raw.githubusercontent.com/andy98725/elo-service/main/docs/elo-service-client.md
```

elo-service is a hosted matchmaking service. Your client gets a JWT, opens a WebSocket to put the player in a queue, and receives a `match_found` payload with the host + ports of a freshly spawned game-server container that it can then connect to directly.

---

## Base URLs

| Use | URL |
|---|---|
| Production / staging HTTP | `https://elomm.net` |
| Production / staging WebSocket | `wss://elomm.net` |
| OpenAPI / Swagger | `https://elomm.net/swagger/index.html` |

There is currently only one hosted environment ("staging" in the repo, but it's the live one). Everything below assumes that base.

---

## Authentication

All endpoints (except registration and login) require a JWT. Pass it either way:

- Header: `Authorization: Bearer <token>` (preferred)
- Query param: `?token=<token>` (use this for WebSocket endpoints from browsers, which can't set custom headers on the WS handshake)

Tokens are HS256 JWTs, valid for 24 hours.

### Guest login (anonymous)

```http
POST /guest/login
Content-Type: application/json

{ "displayName": "PlayerOne" }
```

Response `200`:
```json
{ "token": "eyJhbGciOi…", "displayName": "PlayerOne", "id": "g_<uuid>" }
```

Guest IDs always start with `g_`. The `id` returned here is the **player ID** that game servers will see in their argv (see `elo-service-server.md`). Save it — you'll want to identify yourself with the same string when you connect to the game server.

### User registration

```http
POST /user
Content-Type: application/json

{ "username": "alice", "email": "alice@example.com", "password": "…" }
```

Response `200`:
```json
{ "id": "<uuid>", "username": "alice", "email": "alice@example.com", "is_admin": false, "can_create_game": false }
```

Errors: `400` (missing/invalid fields), `409` (username or email already taken).

### User login

```http
POST /user/login
Content-Type: application/json

{ "email": "alice@example.com", "password": "…" }
```

Login is by email only. The optional `"displayName"` field in the request is echoed back in the response (see quirk below); it is not used for lookup.

Response `200`:
```json
{ "token": "eyJhbGciOi…", "displayName": "alice", "id": "<uuid>" }
```

> **Quirk:** the `displayName` echoed in the response is *whatever you sent in the request*, not the canonical username. If you logged in by email and didn't send a `displayName`, you'll get back an empty string. Use the `id` from this response (or call `GET /user`) if you need the actual username.

Errors: `400` (missing fields), `401` (bad credentials).

### Get the current user

```http
GET /user
Authorization: Bearer <token>
```

Response `200`: same `UserResp` shape as registration.

---

## Game discovery

Every match is for a specific **game** — a registered Docker image + match config. Your client needs the game's UUID to queue.

| Route | Method | Auth | Purpose |
|---|---|---|---|
| `/game/{id}` | GET | user | Fetch one game by UUID |
| `/user/game?page=&pageSize=` | GET | user | List games owned by the current user |

Game listings return:
```json
{
  "games": [
    {
      "id": "<uuid>",
      "owner": { "id": "<uuid>", "username": "alice", "email": "…", "is_admin": false, "can_create_game": true },
      "name": "TicTacToe",
      "description": "…",
      "guests_allowed": true,
      "lobby_enabled": true,
      "lobby_size": 2,
      "matchmaking_strategy": "random",
      "matchmaking_machine_name": "alice/tictactoe:latest",
      "matchmaking_machine_ports": [8080],
      "elo_strategy": "unranked",
      "metadata_enabled": false
    }
  ],
  "nextPage": 2
}
```

There is currently **no public "browse all games" endpoint** — get the game UUID out of band (config, link, etc.) and queue against it.

---

## Matchmaking WebSocket

This is the primary flow for "find me a match."

### Open the connection

```
GET /match/join?gameID=<uuid>&token=<jwt>
```

Optional query params:
- `metadata` — opaque sub-queue key, max **4096 bytes**. Only honored if the game has `metadata_enabled=true`. Players with the same `metadata` value queue together; players with different values don't match. Useful for game-mode/region segmentation. The server hashes it before use.

> **Casing matters.** The query param is `gameID` (camelCase), not `game_id`. Wrong casing is silently dropped and you'll get `"gameID is required"`.

The connection upgrades to WebSocket and starts streaming JSON status messages.

### Status messages

Every message is `{ "status": "<string>", … }`. You'll always see them in roughly this order on a successful match:

```jsonc
// 1. Right after the queue join succeeds.
{ "status": "queue_joined", "players_in_queue": 3 }

// 2. Heartbeat sent every ~5s while waiting in queue.
//    Treat as a keepalive; no other fields.
{ "status": "searching" }

// 3. Lobby filled, container is being spun up.
{ "status": "server_starting", "message": "Match found, waiting for server to start..." }

// 4. Server is ready. This is the terminal success message.
//    The WS will close after this.
{
  "status": "match_found",
  "server_host":  "host-<uuid>.gs.elomm.net",   // OR a raw IPv4 — see below
  "server_ports": [7042, 7043],                  // ints, in the order the game declared them
  "match_id":     "<uuid>"
}
```

On failure, you'll get exactly one error frame and the WS will close:

```json
{ "status": "error", "error": "<reason>" }
```

Common error reasons, by phase:

- **Before queue join** (sent before `queue_joined`): `"gameID is required"`, `"metadata exceeds maximum size"`, `"record not found"` (no game with that UUID), or any underlying queue-join error from the service.
- **After `server_starting`**: `"server not ready"` — the spawned container failed to come up within the health-poll window.

### TTL refresh

You don't need to do anything — the server refreshes the queue TTL for you while the WS stays open. **Just keep the socket open** until you get `match_found` or `error`. Closing the WS removes you from the queue (eventually, via TTL expiry).

### Connecting to the spawned game server

Once you have `match_found`, build the connection URL using `server_host` and the appropriate index of `server_ports`. **The protocol depends on whether you're a browser or a native client.**

#### Native clients (desktop / mobile / dedicated launchers)

You can connect directly:
- TCP: `tcp://<server_host>:<server_ports[i]>`
- UDP: `udp://<server_host>:<server_ports[i]>`
- HTTP: `http://<server_host>:<server_ports[i]>`
- WebSocket: `ws://<server_host>:<server_ports[i]>`

`server_host` will sometimes be an IPv4 (legacy hosts) and sometimes a hostname under `gs.elomm.net` (TLS-enabled hosts). Treat it as opaque — both work for plain `tcp://` / `ws://`.

#### Browser / WebGL clients

Browsers running on an HTTPS page **cannot** open `ws://` or non-TLS connections (mixed-content block). For these clients, the service runs Caddy on every game-server host with a wildcard TLS certificate covering `*.gs.elomm.net`, so that:

- `server_host` will be a hostname like `host-<machine-host-uuid>.gs.elomm.net` (full UUID, not a slug) — already resolves to the host's IP
- TCP-based protocols (HTTP, WebSocket) on `server_ports[i]` are reverse-proxied through Caddy with TLS termination

Connect with:
- `https://<server_host>:<server_ports[i]>` for HTTPS
- `wss://<server_host>:<server_ports[i]>` for secure WebSocket

UDP is **not** TLS-wrapped (browsers can't speak UDP directly anyway, and Caddy doesn't proxy it). If your game uses WebRTC data channels or QUIC, those negotiate their own crypto over UDP and don't need Caddy — point them at `server_host` and the UDP port directly.

#### How do you tell which one you got?

You usually don't need to. Pick based on your runtime:

```js
// Browser / WebGL
const url = `wss://${matchFound.server_host}:${matchFound.server_ports[0]}`;

// Native (Go, C#, C++, etc.)
const url = `ws://${match.server_host}:${match.server_ports[0]}`;
```

If `server_host` looks like an IPv4, the host is non-TLS — only a native client can use it. Production hands out hostnames; if you receive an IP-only `server_host` in a browser, the wildcard-TLS subsystem on the matchmaker is degraded — fail with a clear error.

### Identifying yourself to the game server

The `match_found` payload **does not include an auth code**. Players are identified by their **JWT-derived player ID** — the same `id` you got back from `/guest/login` or `/user/login`. The game server has been told (via its argv) which player IDs to expect, and your client must announce its ID when it connects.

The exact wire format depends on the game-server implementation, but the canonical pattern (used by `example-game-server`) is:

- Send your player ID as the body of `POST /join` over HTTP, or
- Write your player ID followed by `\n` as the first line on TCP

The game server will reject you with `403 Forbidden` if your ID isn't in its expected list (i.e., the matchmaker didn't queue you for that match) and `409 Conflict` if you've already joined.

### Queue size (HTTP)

```http
GET /match/size?gameID=<uuid>&token=<jwt>
```

Response `200`: `{ "players_in_queue": 4 }`. Useful for showing "Searching… (4 players in queue)" UI without having to be in the queue yourself.

---

## Match history & results

After a match ends, the game server reports its result to elo-service, which persists a `MatchResult` record. Clients can read these to show "your last 5 matches," "this game's recent results," or details of a specific match.

| Route | Method | Auth | Purpose |
|---|---|---|---|
| `/match/{matchID}` | GET | user | A live or recently-ended match (participant or game owner only) |
| `/match/game/{gameID}?page=&pageSize=` | GET | user | Paginated matches for a game |
| `/results/{matchID}` | GET | user/guest | One match's final result (winners, reason) |
| `/game/{gameID}/results?page=&pageSize=` | GET | user/guest | Paginated results for a game (filtered to what the caller can see) |
| `/user/results?page=&pageSize=` | GET | user/guest | The caller's own match history |
| `/results/{matchID}/logs` | GET | user/guest | Container stdout for the match (only if the game has `public_match_logs=true`, else owner-only) |

Visibility is enforced server-side per the game's `public_results` flag — non-public games only show results to participants/owners. `404 Not Found` is returned both for missing results and for results the caller can't see (don't infer existence from the status code).

---

## Lobby WebSocket flow

Lobbies are an alternative to matchmaking: a host creates a lobby, players discover it, join it, chat, and the host explicitly starts the match. The post-`/start` handshake is **identical** to matchmaking — same `match_found` payload — so the "connect to the game server" code is reusable.

Lobbies are only available for games with `lobby_enabled=true` (default).

### Host a lobby

```
GET /lobby/host?gameID=<uuid>&tags=tag1,tag2&metadata=<string>&token=<jwt>
```

Optional:
- `tags` — comma-separated, max 16 tags. Searchable via `/lobby/find`.
- `metadata` — opaque string stored on the lobby record (visible to all joiners).

Upgrades to WebSocket. The connecting player **is** the host.

### Find lobbies

```http
GET /lobby/find?gameID=<uuid>&tags=tag1,tag2&token=<jwt>
```

Response `200`:
```json
{
  "lobbies": [
    {
      "id": "<uuid>",
      "game_id": "<uuid>",
      "host_id": "<uuid or g_uuid>",
      "host_name": "PlayerOne",
      "tags": ["pvp", "casual"],
      "metadata": "…",
      "players": 2,
      "max_players": 4,
      "created_at": "…"
    }
  ]
}
```

Filter is **AND** on tags — only lobbies that have *every* requested tag are returned.

### Join a lobby

```
GET /lobby/join?lobbyID=<uuid>&token=<jwt>
```

Upgrades to WebSocket.

### Lobby messages (both host and player connections receive these)

```jsonc
// Sent immediately after the WS upgrade.
// `host: true` only on the connection that called /lobby/host.
// `players` is the *count* of players currently in the lobby (incl. yourself),
// not a list. To know who's in the lobby, either call /lobby/find right
// before joining, or build the roster yourself from player_join /
// player_leave events you receive after this frame.
{
  "status":      "lobby_joined",
  "lobby_id":    "<uuid>",
  "host":        false,
  "host_name":   "PlayerOne",
  "tags":        ["pvp"],
  "metadata":    "…",
  "max_players": 4,
  "players":     2
}

// Another player joined.
{ "event": "player_join",  "id": "g_<uuid>", "name": "PlayerTwo" }

// Player left. `reason` is one of:
//   "left"      — they closed the WS
//   "host_left" — the host disconnected; the lobby is being torn down
//   "kicked"    — host /disconnect'd them
{ "event": "player_leave", "id": "g_<uuid>", "name": "PlayerTwo", "reason": "left" }

// Chat message broadcast (also: any non-/-prefixed text from any player,
// AND any /-prefixed text from a non-host, ends up here verbatim).
{ "event": "player_say",   "id": "g_<uuid>", "name": "PlayerOne", "message": "gg" }

// Host called /start. Match is being created.
{ "event": "lobby_starting" }

// Then the same sequence as matchmaking:
{ "status": "server_starting", "message": "…" }
{ "status": "match_found", "server_host": "…", "server_ports": [7042], "match_id": "<uuid>" }

// You were kicked by the host. The reason is currently the fixed string
// "kicked_by_host" — there's no host-supplied message attached.
{ "status": "kicked", "reason": "kicked_by_host" }

// Any error.
{ "status": "error", "error": "<reason>" }
```

### Sending input as a player

Send a plain-text WS frame (not JSON, no leading `/`) and it gets broadcast as chat:

```
hello everyone
```

Becomes a `player_say` event for everyone in the lobby (including the sender). If a non-host sends a frame that *does* start with `/` (e.g. `/start`), it's still treated as chat — the literal text including the slash is broadcast in `player_say.message`. Host commands only take effect on the host's connection.

### Host commands

The host's WS accepts text commands prefixed with `/`:

| Command | Effect |
|---|---|
| `/disconnect <player_name>` | Kick the named player (lookup is by display name, not ID). They get `{"status": "kicked", "reason": "kicked_by_host"}`, and everyone else gets `player_leave` with `reason: "kicked"`. The host can't kick themselves. |
| `/start` | Create a match with the current set of players, spawn the game server, and broadcast the `match_found` payload to every connected player. The lobby closes after this. **No minimum player count is enforced** — the host can /start with any number of players (even 1), so check the lobby is at capacity before firing if your game requires it. |

### Concurrency note

Lobby capacity is enforced atomically server-side (Lua script in Redis), so two players racing on the last slot can't both succeed. The loser gets `{"status": "error", "error": "lobby is full"}`.

---

## Standard error response (HTTP endpoints)

Non-WebSocket failures return:

```json
{ "message": "<reason>" }
```

with one of these status codes:

| Code | When |
|---|---|
| `400` | Missing or malformed fields |
| `401` | Token missing, invalid, or expired |
| `403` | Not the resource owner / not admin |
| `404` | Resource doesn't exist |
| `409` | Uniqueness conflict (username, email, game name) |
| `500` | Actual server error |

WebSocket failures use the in-band `{"status": "error", "error": …}` frame instead — the HTTP upgrade itself succeeds.

---

## Endpoint cheatsheet

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET`  | `/health` | none | Service liveness probe |
| `POST` | `/guest/login` | none | Anonymous token |
| `POST` | `/user` | none | Register |
| `POST` | `/user/login` | none | Login |
| `GET`  | `/user` | user | Current user info |
| `GET`  | `/game/{id}` | user | Fetch a game by UUID |
| `GET`  | `/user/game` | user | List your games |
| `GET`  | `/match/size` | user/guest | Queue size for a game |
| `GET`  | `/match/join` | user/guest | **WebSocket** matchmaking |
| `GET`  | `/match/{matchID}` | user | Get one match (participant or owner) |
| `GET`  | `/match/game/{gameID}` | user | Paginated matches for a game |
| `GET`  | `/lobby/host` | user/guest | **WebSocket** host lobby |
| `GET`  | `/lobby/find` | user/guest | List lobbies |
| `GET`  | `/lobby/join` | user/guest | **WebSocket** join lobby |
| `GET`  | `/results/{matchID}` | user/guest | One match's result |
| `GET`  | `/results/{matchID}/logs` | user/guest | Download match logs (if public) |
| `GET`  | `/game/{gameID}/results` | user/guest | Paginated results for a game |
| `GET`  | `/user/results` | user/guest | Your own match history |

Full request/response schemas are in the OpenAPI spec at `/swagger/index.html`.

---

## Putting it together (minimal pseudocode)

```python
# 1. Get a token
token = POST("/guest/login", {"displayName": "Anon"})["token"]

# 2. Open the matchmaking WS
ws = WebSocket(f"wss://elomm.net/match/join?gameID={GAME_ID}&token={token}")

# 3. Read frames until match_found or error
while frame := ws.recv():
    msg = json.loads(frame)
    match msg["status"]:
        case "queue_joined" | "searching" | "server_starting":
            update_ui(msg)
        case "match_found":
            connect_to_game(msg["server_host"], msg["server_ports"][0], my_player_id)
            break
        case "error":
            raise RuntimeError(msg["error"])
```

Where `connect_to_game` opens a `ws://` (native) or `wss://` (browser) connection to the game server and announces `my_player_id` per that game server's protocol — see `elo-service-server.md` for what the game server expects.
