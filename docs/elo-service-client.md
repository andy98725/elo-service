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

`displayName` is **required** — calling `/guest/login` without it (or with an empty string) returns `400 "displayName is required"`.

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
{ "token": "eyJhbGciOi…", "displayName": "", "id": "<uuid>" }
```

> **Quirk:** the `displayName` echoed in the *response body* is whatever you sent in the request, not the canonical username — so for a typical email-only login (no `displayName` in the body) the response field is an empty string, as shown above. The JWT itself is unaffected: when `displayName` is omitted, the token's `display_name` claim is backfilled with the user's `username`. Use the JWT claim, the `id` from this response, or `GET /user` if you need the actual username.

Errors: `400` (missing fields), `401` (bad credentials).

### Get the current user

```http
GET /user
Authorization: Bearer <token>
```

Response `200`: same `UserResp` shape as registration.

### Update your profile

```http
PUT /user
Authorization: Bearer <token>
Content-Type: application/json

{ "username": "new-handle", "email": "new@example.com" }
```

Both fields are optional — send only what's changing. Response is the updated `UserResp`.

- `400` — empty string for either field.
- `409` — username or email already taken (the unique-index slot is held even by soft-deleted accounts; see *Delete your account* below).

> **Email is trusted from the client today.** There's no verification round-trip, so a malicious client could change `email` to one it doesn't control. Don't build features that rely on email identity (password reset, ownership transfer, billing) until verification lands.

### Change your password

```http
PUT /user/password
Authorization: Bearer <token>
Content-Type: application/json

{ "current_password": "…", "new_password": "…" }
```

Response `200`: `{ "status": "ok" }`. The current password must match — `401` otherwise. The new password takes effect immediately; re-authenticate with `/user/login` to get a fresh token if you want.

This endpoint always operates on the authenticated user, even when an admin is impersonating someone else. Credential rotation through impersonation isn't allowed by design.

### Delete your account

```http
DELETE /user
Authorization: Bearer <token>
```

Response `200`: `{ "status": "ok" }`. This is a **soft delete** — the row stays in the database so match history and ratings remain intact, but the account can no longer log in and is hidden from listings. **Username and email are not released** — re-registering with either returns `409`. There is currently no self-service path to undelete; an admin has to do it via direct DB access.

Existing JWTs issued to the account before deletion will fail on the next request that goes through user middleware (the lookup filters out deleted rows). Clients should drop the token and return the user to the login screen.

---

## Game discovery

Every match is for a specific **game** — a registered Docker image + match config. Your client needs the game's UUID to queue.

| Route | Method | Auth | Purpose |
|---|---|---|---|
| `/game/{id}` | GET | none | Fetch one game by UUID (public) |
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
      "public_results": true,
      "public_match_logs": false,
      "spectate_enabled": false,
      "queues": [
        {
          "id": "<queue uuid>",
          "game_id": "<uuid>",
          "name": "primary",
          "lobby_enabled": true,
          "lobby_size": 2,
          "matchmaking_strategy": "random",
          "matchmaking_machine_name": "alice/tictactoe:latest",
          "matchmaking_machine_ports": [8080],
          "elo_strategy": "unranked",
          "default_rating": 1000,
          "k_factor": 32,
          "metadata_enabled": false
        }
      ]
    }
  ],
  "nextPage": 1
}
```

Each game has an ordered `queues` array. The first element (`queues[0]`) is the **default queue** — the one used when matchmaking, lobby, or rating endpoints are called without an explicit `queueID`. Single-queue games will only ever have one entry; multi-queue games (different game modes / images / ELO settings under one game) iterate `queues` and let the player pick.

`nextPage` is the index to pass as `page` on the next call, **or `-1` if you've reached the end of the list** (the page returned fewer rows than `pageSize`). Stop paginating when you see `-1`.

There is currently **no public "browse all games" endpoint** — get the game UUID out of band (config, link, etc.) and queue against it.

---

## Matchmaking WebSocket

This is the primary flow for "find me a match."

### Open the connection

```
GET /match/join?gameID=<uuid>&token=<jwt>
```

Optional query params:
- `queueID` — specific GameQueue UUID for multi-queue games (e.g. choose between `casual` and `ranked`). When omitted, defaults to the game's primary queue (`queues[0]`). Single-queue games can ignore this.
- `metadata` — opaque sub-queue key, max **4096 bytes**. Only honored if the resolved queue has `metadata_enabled=true`. Players with the same `metadata` value queue together; players with different values don't match. Useful for further region/mode segmentation within a queue. The server hashes it before use.

> **Casing matters.** The query params are `gameID` and `queueID` (camelCase), not `game_id` / `queue_id`. Wrong casing is silently dropped and you'll get `"gameID is required"`.

The connection upgrades to WebSocket and starts streaming JSON status messages.

### Status messages

Every message is `{ "status": "<string>", … }`. You'll always see them in roughly this order on a successful match:

```jsonc
// 1. Right after the queue join succeeds.
{ "status": "queue_joined", "players_in_queue": 3 }

// 2. Heartbeat sent every ~5s while the WS is open. Treat as a keepalive.
{ "status": "searching" }

// 3. Lobby filled, container is being spun up.
{ "status": "server_starting", "message": "Match found, waiting for server to start..." }

// 4. Server is ready. This is the terminal success message.
//    The WS will close after this.
{
  "status":        "match_found",
  "server_host":   "host-<uuid>.gs.elomm.net",   // OR a raw IPv4 — see below
  "server_ports":  [7042, 7043],                  // ints, in the order the game declared them
  "match_id":      "<uuid>",
  "connect_token": "<opaque string>"             // join credential for the game server
}
```

`connect_token` is the per-player credential the game server expects when the client joins the match. Its value currently equals the player's ID; a planned change replaces it with an opaque per-(match, player) secret under the same field name.

> **Heartbeat continues across phases.** The same 5s ticker that emits `{"status": "searching"}` keeps firing through `server_starting` too: once the queue fills, you'll see one `server_starting` frame *with* the `message` field (shown above), then bare `{"status": "server_starting"}` heartbeats every ~5s until `match_found`. Don't treat duplicate `server_starting` frames as a bug.

On failure, you'll get exactly one error frame and the WS will close:

```json
{ "status": "error", "error": "<reason>" }
```

Common error reasons, by phase:

- **Before queue join** (sent before `queue_joined`): `"gameID is required"`, `"metadata exceeds maximum size"`, `"record not found"` (no game with that UUID), or any underlying queue-join error from the service.
- **After `server_starting`**: `"server not ready"` — the spawned container failed to come up within the health-poll window.

### TTL refresh

You don't need to do anything — the server refreshes the queue TTL for you while the WS stays open. **Just keep the socket open** until you get `match_found` or `error`. Closing the WS removes you from the queue (eventually, via TTL expiry).

### Leaving the queue (/disconnect)

To leave the queue cleanly without waiting for TTL cleanup, send a single text frame:

```
/disconnect
```

The server responds with `{"status": "disconnected"}`, removes you from the queue immediately, and closes the WebSocket. This is preferable to just closing the socket: TCP close removes you only on the next GC sweep (up to ~3 minutes later), while `/disconnect` is synchronous.

### WebSocket keepalive (Ping/Pong)

The matchmaking and lobby WSes run server-driven keepalive: the server sends an [RFC 6455 Ping control frame](https://datatracker.ietf.org/doc/html/rfc6455#section-5.5.2) every **30 seconds** and expects a Pong back within **75 seconds**. This is independent of the JSON `{"status": "searching"}` heartbeat — pings are at the WS protocol layer, not application messages.

WS libraries that auto-reply to pings (browser `WebSocket`, Go `gorilla/websocket` with the default `PongHandler`, Node.js `ws`, most C# / Unity / Unreal libraries, Python `websockets`) need no special handling. Low-level libraries require an explicit Pong response from a ping handler.

The server has two modes for missed-pong handling, controlled by the `WSLivenessDisconnectEnabled` config flag:

- **Soft mode** (`false`, default): pings are sent and pong arrivals tracked, but clients that don't pong are not disconnected — missed pongs are logged.
- **Hard mode** (`true`): a WS that misses pongs for 75 s is closed by the server with a normal WS close frame.

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

The `match_found` payload carries a `connect_token` — the credential the client presents to the game server on join. The matchmaker has handed the game server (via argv) the set of connect tokens admissible for this match; the client sends back the value it received.

The exact wire format depends on the game-server implementation. The canonical pattern (used by `example-game-server`) is:

- HTTP: `POST /join` with `connect_token` as the request body (plain text).
- TCP: `connect_token` followed by `\n` as the first line.

A game server rejects unknown tokens with `403 Forbidden` and tokens that have already joined with `409 Conflict`.

`connect_token` currently equals the player's ID, so the wire bytes match the previous protocol where clients sent the player ID directly. A planned change replaces the value with an opaque per-(match, player) secret; the field name and the validation rule do not change.

### Queue size (HTTP)

```http
GET /match/size?gameID=<uuid>&queueID=<uuid>&metadata=<string>&token=<jwt>
```

Response `200`: `{ "players_in_queue": 4 }`. Useful for showing "Searching… (4 players in queue)" UI without having to be in the queue yourself.

`queueID` is optional — defaults to the game's primary queue. Pass it when polling a non-default queue under a multi-queue game.

`metadata` is optional and follows the same rules as on `/match/join` — only honored when the resolved queue has `metadata_enabled=true`, max 4096 bytes, and segments the count to the matching sub-queue. For metadata-segmented queues, calling `/match/size` without `metadata` returns the *empty* sub-queue's size, which is rarely what you want.

### Reconnecting after a page reload

The matchmaking WebSocket emits `match_found` exactly once. If the connection drops or the user reloads, the original payload is gone — the client needs another way to find the running game server. Use:

```http
GET /games/<gameID>/match/me
Authorization: Bearer <jwt>
```

Response `200`:

```json
{
  "matches": [
    {
      "match_id":      "<uuid>",
      "server_host":   "host-...gs.elomm.net",
      "server_ports":  [7001],
      "started_at":    "2026-04-29T08:24:04Z",
      "connect_token": "<opaque string>"
    }
  ]
}
```

The shape inside `matches[]` mirrors the `match_found` payload — feed it into the same connection code. An empty `matches` array means no active match; treat as "not in a game."

A player can be in multiple started matches in the same game at once (the matchmaker doesn't enforce one-at-a-time), so the response is a list. Most games will see at most one entry; if you need to pick, sort by `started_at` and use the most recent.

**Guest caveat.** Guest identity lives entirely in the JWT. If the page reloads without preserving the token (localStorage, cookie, or wherever your client stashes it), `/guest/login` mints a new ID and this endpoint returns empty for the new identity. Native clients with stable storage are unaffected; browser clients should persist the token before they need to reconnect.

### Discovering live matches (spectator)

Games can opt into letting non-participants discover ongoing matches by setting `spectate_enabled=true` at game creation (or update). When that flag is on:

```http
GET /games/<gameID>/matches/live
Authorization: Bearer <token>
```

Response `200`:

```json
{
  "matches": [
    {
      "match_id":   "<uuid>",
      "started_at": "2026-04-29T08:24:04Z",
      "players":    ["<user-uuid>", …],
      "guest_ids":  ["g_…", …],
      "has_stream": false
    }
  ]
}
```

`404` when the game itself doesn't have `spectate_enabled` (regardless of the caller — it's the game's choice, not a per-user permission). Empty `matches` array when no started matches exist.

`has_stream` answers "is this match actually streaming bytes right now?" — wire it to your "Watch" button. The matchmaker probes the S3 manifest to decide; it flips `true` once the uploader has written its first chunk (typically within a second of match start) and stays `true` for the life of the match. A spectate-enabled match with `has_stream: false` means the game server hasn't written to `/shared/spectate.stream` yet. The connection details (server host/ports) are deliberately not in this response — spectators don't dial the game server directly; they consume the matchmaker-proxied stream at `/matches/<matchID>/stream`.

**Per-match override (lobbies only).** A lobby host can disable spectating on a single match by passing `?spectate=false` to `/lobby/host`. The flag is **disable-only** — passing `spectate=true` on a non-spectate game does nothing. Matches paired through the matchmaking queue inherit the game flag with no override.

### Tailing a spectator stream

Once a match shows `has_stream: true` in discovery, you can tail its near-live byte stream via:

```http
GET /matches/<matchID>/stream?cursor=<int>
Authorization: Bearer <token>
```

- **First call:** pass `cursor=0`. The server returns whatever bytes the game server has produced so far (concatenated chunks).
- **Subsequent calls:** pass the value of the `X-Spectate-Cursor` response header from the prior call. The server returns only bytes produced since then.
- **Long-poll:** when the cursor is caught up, the request blocks for up to ~30 seconds before returning. If new bytes arrive in that window, you get them immediately. Otherwise the response is empty (200 + 0 bytes, same cursor).
- **EOF:** once `X-Spectate-EOF: true` appears, the match is over and no more bytes are coming. Stop polling.

The body is `application/octet-stream` — **opaque bytes whose format is defined by the game server**, not the matchmaker. You decode them the same way the game's own client does (NDJSON of state diffs, length-prefixed binary frames, plain text — whatever the game emits to `/shared/spectate.stream`).

```js
async function tailSpectatorStream(matchID, token, onBytes) {
  let cursor = 0;
  while (true) {
    const r = await fetch(`/matches/${matchID}/stream?cursor=${cursor}`, {
      headers: { Authorization: `Bearer ${token}` }
    });
    if (r.status === 404) break;             // not spectatable / match gone
    const eof = r.headers.get("X-Spectate-EOF") === "true";
    cursor = parseInt(r.headers.get("X-Spectate-Cursor"), 10);
    const buf = new Uint8Array(await r.arrayBuffer());
    if (buf.length > 0) onBytes(buf);
    if (eof) break;
  }
}
```

**Latency.** Round-trip is roughly `chunk_interval (~1s) + S3 RTT + your poll interval` — typically 5–15 seconds behind the live game. Don't promise real-time spectating; this is a delayed broadcast.

**Auth.** User or guest tokens both work. There's no per-spectator limit yet — that's a future-PR concern when load actually warrants it.

**Replay archive.** When a match ends, the matchmaker moves the chunks out of the live tier into a replay archive and finalizes the manifest. The same `/matches/<matchID>/stream` endpoint serves the replay — your client doesn't need a different code path. The replay returns `X-Spectate-EOF: true` immediately on first poll. Replays are **kept indefinitely** today, so a `match_id` from days, weeks, or months ago should still tail successfully. (If retention ever changes, this doc will too.)

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
| `/results/{matchID}/logs` | GET | user (owner/admin only) | Container stdout for the match — restricted to the game's owner and site admins |

Visibility is enforced server-side per the game's `public_results` flag — non-public games only show results to participants/owners. The result-fetch routes return `404 Not Found` both for missing results and for results the caller can't see (don't infer existence from the status code).

`/results/{matchID}/logs` is locked down to the game's owner and site admins. Anyone else — including match participants — gets `404 Not Found` (existence is hidden, by design). Guest tokens are rejected with `401`. The legacy `Game.public_match_logs` flag is no longer consulted; setting it has no effect on this route.

### Match artifacts

A game server can attach named artifacts to a match — replay files, preview images, highlight reels, anything else. The platform doesn't interpret the bytes; each artifact has a name (`[a-zA-Z0-9._-]{1,64}`), a content-type (preserved from upload), and a download URL.

**List one match's artifacts:**

```http
GET /matches/<matchID>/artifacts
Authorization: Bearer <token>
```

```json
{
  "artifacts": {
    "preview": {
      "content_type": "image/png",
      "size_bytes":   12345,
      "uploaded_at":  "2026-04-29T10:00:00Z",
      "url":          "/matches/<matchID>/artifacts/preview"
    },
    "replay": { "content_type": "application/octet-stream", "size_bytes": 4096, "uploaded_at": "...", "url": "..." }
  }
}
```

**Download:** follow the `url` (relative to the API base) — the response carries the original Content-Type, so `<img src=…>` works for previews and `fetch().arrayBuffer()` works for replay binaries. Same `404`-on-not-visible rule as the rest of `/results/...` (auth gated by `Game.public_results` and participation).

**List your matches that have artifacts:**

```http
GET /user/artifacts?game_id=<gameID>&name=replay&name=preview&page=0&pageSize=10
Authorization: Bearer <token>
```

Both query params are optional:
- `game_id` — restrict to a single game
- `name` — repeatable; OR-filter (matches keep entries where at least one of these names exists). Each returned match still shows its **full** artifact set, not just the filtered names

```json
{
  "matches": [
    {
      "match_id":  "<uuid>",
      "game_id":   "<uuid>",
      "ended_at":  "2026-04-29T10:00:00Z",
      "artifacts": { "preview": {...}, "replay": {...} }
    }
  ],
  "next_page": 1
}
```

`next_page` is `-1` once you've reached the last page (mirrors the existing pagination convention). Artifacts persist for the life of the `MatchResult`; there's no separate retention policy beyond whatever applies to match history.

Conventional names games may upload (recommended, not enforced):
- `preview` — small image for match-history thumbnails
- `replay` — game-defined replay file the original client can re-render

---

## Lobby WebSocket flow

Lobbies are an alternative to matchmaking: a host creates a lobby, players discover it, join it, chat, and the host explicitly starts the match. The post-`/start` handshake is **identical** to matchmaking — same `match_found` payload — so the "connect to the game server" code is reusable.

Lobbies are only available for queues with `lobby_enabled=true` (default). The lobby inherits its `lobby_size`, image, and ELO settings from the queue it's hosted under.

### Host a lobby

```
GET /lobby/host?gameID=<uuid>&queueID=<uuid>&tags=tag1,tag2&metadata=<string>&password=<string>&private=<bool>&spectate=<bool>&token=<jwt>
```

Optional:
- `queueID` — specific GameQueue UUID. Defaults to the game's primary queue when omitted. The match started from this lobby uses that queue's image, ports, lobby size, and ELO settings.
- `tags` — comma-separated, **first 16 tags kept** (extras silently dropped, not rejected). Searchable via `/lobby/find`.
- `metadata` — opaque string stored on the lobby record (visible to all joiners).
- `password` — when set, joiners must supply the same value on `/lobby/join` to enter. Stored bcrypt-hashed; max 72 bytes (bcrypt's input limit). Only the boolean `password_protected` is exposed on `/lobby/find` — the hash never leaves the server.
- `private` — when truthy (`1`/`true`), the lobby is **excluded from `/lobby/find`**. Joiners must be given the lobby ID directly (e.g. via an out-of-band invite link). Effectively "unlisted" — anyone with the ID can still `/lobby/join`, so combine with `password` if you want both link-secrecy and a join gate.
- `spectate` — per-match override of the game's `spectate_enabled`. **Disable-only**: pass `false` to keep this match out of `/games/<gameID>/matches/live` even on a spectate-enabled game. Passing `true` on a non-spectate game has no effect. Default: inherit the game flag.

Upgrades to WebSocket. The connecting player **is** the host. The host's `lobby_joined` ack echoes back `"private": <bool>` so you can confirm what was created.

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
      "created_at": "…",
      "password_protected": false
    }
  ]
}
```

Filter is **AND** on tags — only lobbies that have *every* requested tag are returned. `password_protected` is `true` when the host created the lobby with a `password`; the hash itself is never returned. **Lobbies hosted with `private=true` are not listed here at all** — they have to be joined directly via their lobby ID.

### Join a lobby

```
GET /lobby/join?lobbyID=<uuid>&password=<string>&token=<jwt>
```

Upgrades to WebSocket.

`password` is required only for lobbies whose `/lobby/find` entry has `password_protected: true`. A missing or wrong password is rejected with `{"status": "error", "error": "invalid password"}` (single message for both cases — by design, so probing can't distinguish "no password sent" from "wrong password"). The check runs before the capacity gate, so failed attempts never occupy a slot.

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

// Acknowledgment that the server processed your /disconnect. The WS
// closes immediately after this frame.
{ "status": "disconnected" }

// Any error.
{ "status": "error", "error": "<reason>" }
```

### Sending input as a player

Send a plain-text WS frame (not JSON, no leading `/`) and it gets broadcast as chat:

```
hello everyone
```

Becomes a `player_say` event for everyone in the lobby (including the sender). If a non-host sends a frame that *does* start with `/` (e.g. `/start`), it's still treated as chat — the literal text including the slash is broadcast in `player_say.message`. The exception is `/disconnect` (see below). Host commands only take effect on the host's connection.

### Leaving a lobby (/disconnect)

Either side — host or player — can send a single bare `/disconnect` text frame to leave cleanly. The server responds with `{"status": "disconnected"}` and closes the WebSocket. Other lobby members receive a `player_leave` event:

- Player leaves: `{"event": "player_leave", "id": "…", "name": "…", "reason": "left"}`.
- Host leaves: `{"event": "player_leave", "id": "…", "name": "…", "reason": "host_left"}` and the lobby is torn down — every other connection receives that frame too and the WS will close shortly after.

This is the same outcome as closing the WS, but explicit; it's the recommended way to leave when your UI offers a "Leave lobby" button.

### Host commands

The host's WS accepts text commands prefixed with `/`:

| Command | Effect |
|---|---|
| `/disconnect` | (No arg.) Host leaves the lobby, which tears it down. See "Leaving a lobby" above. |
| `/disconnect <player_name>` | Kick the named player (lookup is by display name, not ID). They get `{"status": "kicked", "reason": "kicked_by_host"}`, and everyone else gets `player_leave` with `reason: "kicked"`. The host can't kick themselves. |
| `/start` | Create a match with the current set of players, spawn the game server, and broadcast the `match_found` payload to every connected player. The lobby closes after this. **No minimum player count is enforced** — the host can /start with any number of players (even 1), so check the lobby is at capacity before firing if your game requires it. |

### Concurrency note

Lobby capacity is enforced atomically server-side (Lua script in Redis), so two players racing on the last slot can't both succeed. The loser gets `{"status": "error", "error": "lobby is full"}`.

---

## Per-player game data (settings, achievements, etc.)

elo-service includes a tiny per-(game, player) JSON key-value store you can read and write from the client. It's intended for things like player settings, cosmetic preferences, or any state the client wants to persist server-side without standing up its own backend.

Each (game, player) has **two independent namespaces** sharing the same key space:

- **Player-authored** — you write these from the client. The game server can read them but can't modify them.
- **Server-authored** — the game server writes these (achievements, levels, last seen score, etc.). You can read them from the client but can't modify them.

The same key can exist in both namespaces with different values; they don't collide. That's why the read endpoints are split — you GET each side separately so you always know which half is authoritative.

**Guests cannot use this feature.** All endpoints below require a registered-user JWT and return `401` for guest tokens. There's no persistent identity to attach data to once a guest token expires, so we don't try.

### Read your own entries

```http
GET /games/{gameID}/data/me/player
GET /games/{gameID}/data/me/server
Authorization: Bearer <user token>
```

Both return:
```json
{ "entries": { "settings": {"audio": 0.7}, "color": "blue" } }
```

`entries` is a keyed object (not an array) — values are arbitrary JSON. Empty object `{}` if nothing's been written yet.

### Write a player-authored entry

```http
PUT /games/{gameID}/data/me/{key}
Authorization: Bearer <user token>
Content-Type: application/json

<arbitrary JSON value>
```

The request body **is** the value — no envelope, no `{"value": …}` wrapper. Examples:

- `PUT …/me/settings` body `{"audio":0.7,"theme":"dark"}` → stored as that object.
- `PUT …/me/last_seen_changelog` body `"2026-04-28"` → stored as that string.
- `PUT …/me/level_progress` body `[1,2,3,4]` → stored as that array.

Replaces any existing player-authored entry at the same key. Does not affect a server-authored entry at the same key.

Response `200`: `{"status":"ok"}`.

### Delete a player-authored entry

```http
DELETE /games/{gameID}/data/me/{key}
Authorization: Bearer <user token>
```

Response `200` if the entry existed, `404` if it didn't. You can only delete entries you wrote (player-authored side); use the server's API to delete server-authored entries.

### Limits & validation

| Constraint | Limit |
|---|---|
| Key format | `[a-zA-Z0-9._-]{1,128}` — letters, digits, dot, underscore, hyphen, max 128 chars |
| Value size | 64 KB serialized JSON |
| Value format | Must be valid JSON. `null`, numbers, strings, arrays, objects all OK. Empty body is rejected. |

Error codes:
- `400` — key doesn't match the regex, or body isn't valid JSON.
- `401` — missing token, or you sent a guest token.
- `404` — DELETE on a key that doesn't exist.
- `413` — value larger than 64 KB.

### Use cases

- **Settings** — controls, audio, accessibility. Player-authored, persisted across sessions/devices.
- **Last-seen markers** — "I've seen the changelog through 2026-04-28," "I've dismissed tutorial X."
- **Server-issued state** — your game server can publish achievements, current level, cosmetic unlocks; the client reads these via `…/me/server` to render UI without separately tracking them.

For game-server-side writes (achievements, ratings beyond Elo, anything the server should be authoritative on), see the corresponding section in `elo-service-server.md`.

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
| `PUT`  | `/user` | user | Update own username / email (admin: `?id=<uuid>` + `can_create_game`) |
| `PUT`  | `/user/password` | user | Rotate own password (verifies current) |
| `DELETE` | `/user` | user | Soft-delete own account (admin: `?id=<uuid>`) |
| `GET`  | `/game/{id}` | none | Fetch a game by UUID (public) — includes `queues[]` array |
| `GET`  | `/user/game` | user | List your games |
| `GET`  | `/game/{gameID}/queue` | none | List queues for a game (oldest first; `[0]` is the default) |
| `GET`  | `/game/{gameID}/queue/{queueID}` | none | Fetch a single queue |
| `GET`  | `/match/size` | user/guest | Queue size — accepts optional `queueID` (defaults to primary queue) |
| `GET`  | `/match/join` | user/guest | **WebSocket** matchmaking — accepts optional `queueID` |
| `GET`  | `/match/{matchID}` | user | Get one match (participant or owner) |
| `GET`  | `/match/game/{gameID}` | user | Paginated matches for a game |
| `GET`  | `/games/{gameID}/match/me` | user/guest | Active matches you're in (for reconnect) |
| `GET`  | `/games/{gameID}/matches/live` | user/guest | Spectatable live matches (404 if game `spectate_enabled=false`) |
| `GET`  | `/matches/{matchID}/stream` | user/guest | Long-poll spectator stream (404 if match `spectate_enabled=false`) |
| `GET`  | `/matches/{matchID}/artifacts` | user/guest | List artifacts attached to a match (gated by `public_results`) |
| `GET`  | `/matches/{matchID}/artifacts/{name}` | user/guest | Download one artifact's bytes |
| `GET`  | `/user/artifacts` | user/guest | Your matches that have artifacts; optional `game_id` and `name=` filters |
| `GET`  | `/lobby/host` | user/guest | **WebSocket** host lobby — accepts optional `queueID` |
| `GET`  | `/lobby/find` | user/guest | List lobbies |
| `GET`  | `/lobby/join` | user/guest | **WebSocket** join lobby |
| `GET`  | `/user/rating/{gameId}` | user | Your rating in a queue (optional `queueID`, default primary) |
| `GET`  | `/game/{gameId}/leaderboard` | none | Top-rated players in a queue (optional `queueID`, default primary) |
| `GET`  | `/results/{matchID}` | user/guest | One match's result |
| `GET`  | `/results/{matchID}/logs` | user (owner/admin only) | Download match logs — owner of the game or site admin only |
| `GET`  | `/game/{gameID}/results` | user/guest | Paginated results for a game |
| `GET`  | `/user/results` | user/guest | Your own match history |
| `GET`  | `/games/{gameID}/data/me/player` | user | Your player-authored entries for this game |
| `GET`  | `/games/{gameID}/data/me/server` | user | Server-authored entries about you for this game |
| `PUT`  | `/games/{gameID}/data/me/{key}` | user | Upsert a player-authored entry |
| `DELETE` | `/games/{gameID}/data/me/{key}` | user | Delete a player-authored entry |

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
