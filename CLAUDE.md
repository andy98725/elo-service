# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

`elo-service` is a Go matchmaking service backed by Postgres (persistent: users, games, ratings, match records) and Redis (transient: queue, pubsub triggers, per-player TTLs). The hosted instance lives on **fly.io** (app: `elo-service`), Postgres on **Neon**, Redis on **Upstash**, and game-server VMs on **Hetzner Cloud**.

There is currently only a staging environment. The `production` branch is the deploy line: pushes to `production` build and ship via Fly. Pushes to `main` and PRs run tests but do **not** deploy. The model is "main is ready-to-ship; production is the explicit release gate."

## Repository layout

This repo is **four Go modules**, not one:

- [`src/`](src/) — the elo-matchmaking service (root [`go.mod`](go.mod), module `github.com/andy98725/elo-service`). Entrypoint [`src/main.go`](src/main.go).
- [`game-server-host-agent/`](game-server-host-agent/) — HTTP agent that runs on each Hetzner host VM. The matchmaker calls it to start/stop game-server Docker containers on that host. Own [`go.mod`](game-server-host-agent/go.mod).
- [`game-server-sidecar/`](game-server-sidecar/) — minimal HTTP server that tails `/logs/server.log`. Runs alongside game containers so the matchmaker can pull match logs. Own [`go.mod`](game-server-sidecar/go.mod).
- [`example-game-server/`](example-game-server/) — reference game-server impl (Docker image `andy98725/example-server:latest`) used by e2e tests. Own [`go.mod`](example-game-server/go.mod).

## Architecture

### Host-pool model

The matchmaker provisions long-lived Hetzner VMs ("hosts") that each run multiple game-server containers. A new match either lands on an existing host with free capacity or triggers a fresh host provision (capped by `HCLOUD_MAX_HOSTS` × `HCLOUD_MAX_SLOTS_PER_HOST`). Two models persist this:

- [`MachineHost`](src/models/machine_host.go) — one row per Hetzner VM. Tracks public IP, the agent token, allocated host ports, status.
- [`ServerInstance`](src/models/server_instance.go) — one row per game container. References its `MachineHost`, the agent-issued container ID, the game and host port pairs, and a per-match `auth_code`.

A `Match` references a `ServerInstance`, which references a `MachineHost`. `match.ConnectionAddress()` joins through both.

### Match-found request flow

1. Client opens a WebSocket on `/match/join?gameID=…&metadata=…` with a JWT.
2. [`src/api/match/matchmaking.go`](src/api/match/matchmaking.go) calls `matchmaking.JoinQueue`, which writes the player into a Redis list (`queue_<queueID>`, where `queueID` may be `gameID` or `gameID::<sha256(metadata)>` for metadata-segmented games — see PR #2).
3. The worker goroutine ([`src/worker/worker.go`](src/worker/worker.go)) wakes on a pubsub trigger and runs `matchmaking.PairPlayers` (rate-limited by `MATCHMAKING_INTERVAL`, default 100 ms).
4. When the queue reaches `LobbySize`, the worker calls `matchmaking.StartMatch`:
   - finds an available host (or provisions one),
   - allocates host ports for the game's port count,
   - tells the agent to start a container via `POST /containers`,
   - persists the `ServerInstance` and `Match` rows,
   - publishes `match_<matchID>` on `match_ready_<gameID>__<playerID>` for each player.
5. The matchmaker WebSocket subscribes to that channel via `matchmaking.NotifyOnReady`. On receipt it polls the agent's `/containers/<id>/health` until ready, then pushes the final message:
   ```json
   { "status": "match_found",
     "server_host":  "<host_public_ip>",
     "server_ports": [<host_port>, …],
     "match_id":     "<uuid>" }
   ```
   No `auth_code` — players are identified by their JWT-derived player IDs on the game server side (see PR #5 changes).

### Lobby flow

[`src/api/lobby/`](src/api/lobby/) — three WebSocket routes (`/lobby/host`, `/lobby/find`, `/lobby/join`) plus chat/`/disconnect`/`/start` host commands. Reuses the same `match_ready_<gameID>__<playerID>` pubsub channel as matchmaking, so post-`/start` clients get the identical `match_found` handshake.

Capacity is enforced atomically via a Lua script in [`src/external/redis/lobby.go`](src/external/redis/lobby.go) — concurrent joiners can't both pass the cap check.

### Worker

Single in-process goroutine in [`src/worker/worker.go`](src/worker/worker.go). Two pubsub triggers (`matchmaking`, `garbage_collection`) wake it; an interval floor on each prevents thrash. The GC pass runs `GarbageCollectMatches`, `CleanupExpiredPlayers`, `CleanupExpiredLobbies`, and `MaintainWarmPool` in sequence.

### Warm pool

[`src/worker/matchmaking/warmPool.go`](src/worker/matchmaking/warmPool.go) keeps at least `HCLOUD_WARM_SLOTS` container slots ready across already-provisioned hosts (capped by `HCLOUD_MAX_HOSTS`). Runs at startup and on every GC tick. With `HCLOUD_WARM_SLOTS=0`, every match pays cold-start (~30-60 s). Currently set to `1` on the staging Fly app.

### Per-(game, player) data store

[`src/api/playerData/`](src/api/playerData/) implements a dynamic per-(game, player) JSON KV backend that game servers can write to and players can read + write. The model is one table — [`PlayerGameEntry`](src/models/player_game_entry.go) — with PK `(game_id, player_id, key, server_authored)`. Putting `server_authored` in the PK gives two independent namespaces sharing a (game, player): a player-authored entry and a server-authored entry can both exist at the same key without collision.

**Auth:** route-based, not per-row. The route the request comes in on dictates which namespace it touches.
- Player-side routes (`/games/:gameID/data/me/...`) require user JWT auth (`RequireUserAuth`). They read both halves but only write `server_authored = false`.
- Server-side routes (`/games/:gameID/data/:playerID/...`) require the active match's `auth_code` in the `Authorization: Bearer <code>` header — same code the game server uses for `/result/report`. The `requireMatchAuth` helper in [`serverSide.go`](src/api/playerData/serverSide.go) resolves the match, verifies it's still underway, that the URL `gameID` matches, and that `playerID` is in the match's `Players`. Server routes write `server_authored = true` only.

**Guests are intentionally rejected.** Guest IDs are not persisted in `users` (no row, no FK target, no recovery path when the ephemeral token is lost), and anyone can mint one for free. Player-side routes refuse the guest JWT (`RequireUserAuth` doesn't accept it). Server-side routes refuse a `:playerID` that starts with `g_` (400). The game server can still observe guests in its match via `Match.GuestIDs` — only KV writes for them are blocked.

**Limits:** key matches `[a-zA-Z0-9._-]{1,128}`, value is any valid JSON up to 64KB. Both enforced in handlers (not the schema), so they can be changed without a migration. List endpoints return all entries for the (game, player, side) — no pagination yet; revisit if a single (game, player) starts accumulating hundreds of keys.

**Lifecycle:** `Game` and `Player` FKs are `OnDelete:CASCADE`, so deleting a game or user cleans up its entries. **Postgres-only** — the SQLite test harness does not enable `PRAGMA foreign_keys`, so the cascade is not exercised in integration tests; verify in a real Postgres run when changing the FK behavior.

### Singletons

`server.S` is a package-level global ([`src/server/server.go`](src/server/server.go)) holding the DB, Redis, AWS, Hetzner, and (eventually) DNS/Cert clients. Models reach into `server.S.DB` directly — there is no repository abstraction. The Hetzner client is wired through the [`MachineService`](src/server/interfaces.go) interface so integration tests can inject a mock that simulates the agent.

### Migrations

[`src/models/migrations.go`](src/models/migrations.go) uses `gormigrate` wrapped in `pg_advisory_lock(42)` so multiple Fly machines starting concurrently don't race on schema. After named migrations, an unconditional `AutoMigrate` runs over every model — so adding a column to a struct ships without a new migration step. The named migrations exist for destructive schema changes (e.g. dropping the old per-match `machine_*` columns when the host-pool model landed).

## Rolling out a new host-agent image

Cloud-init pulls `andy98725/game-server-host-agent:latest` **at host provisioning time** ([`src/external/hetzner/machines.go`](src/external/hetzner/machines.go)) and never refreshes. So existing hosts stay frozen at the agent version they booted with; only newly-provisioned hosts pick up updates. Rolling a change to the live fleet is a deliberate sequence:

1. **Build + push the new image:** `make agent-push` (builds [`game-server-host-agent/`](game-server-host-agent/), tags as `andy98725/game-server-host-agent:latest`, pushes to Docker Hub).
2. **Find an empty host** — a `MachineHost` with `status='ready'` and no active `ServerInstance` rows. SQL:
   ```sql
   SELECT mh.id, mh.provider_id, mh.public_ip, COUNT(si.id) FILTER (WHERE si.status != 'deleted') AS active
   FROM machine_hosts mh LEFT JOIN server_instances si ON si.machine_host_id = mh.id
   WHERE mh.status = 'ready' GROUP BY mh.id;
   ```
   Only roll hosts where `active = 0`. Rolling a host with running matches drops live players.
3. **Delete the Hetzner VM directly** (provider console or `hcloud server delete <providerID>`). The matchmaker sees nothing yet — the row is still `status='ready'`.
4. **Wake the GC trigger** so the matchmaker reconciles the dead VM and the warm pool refills with a freshly-provisioned host (which pulls the new image). With idle traffic, GC doesn't fire on its own ([`src/worker/matchmaking/pairPlayers.go`](src/worker/matchmaking/pairPlayers.go) only publishes the trigger after pairings); send a manual pulse:
   ```
   PUBLISH trigger_garbage_collection 1
   ```
   on the production Redis (Upstash). Or trigger any real match — same effect.
5. **Watch for the new row**: a fresh `machine_hosts` entry with a different `provider_id` should appear within ~60–90s, status `provisioning` then `ready`. Verify the new agent by hitting an endpoint that exists only on the new build (e.g. a new route added in the change).

The `MaintainWarmPool` pass only grows the pool; there's no automatic shrink. The destroy-and-let-warm-pool-refill dance is the only way to roll the agent on existing hosts. Repeat per host if rolling more than one.

## Auth model

JWT-based, two token types validated in [`src/api/auth/middleware.go`](src/api/auth/middleware.go):

- **User token** — registered users; `claims.UserID` is a UUID. Admins (`user.IsAdmin`) can set `ImpersonationID` in the claim to act as another user or guest.
- **Guest token** — anonymous; `claims.ID` starts with `g_`. Use [`util.IsGuestID`](src/util/guest.go) to distinguish.

Use `RequireUserOrGuestAuth` for endpoints that accept either; `c.Get("id")` always returns the effective player ID after impersonation resolution.

## Testing

Two test suites:

- **`tests/integration/`** (preferred for new work) — boots the full server in-process per test using miniredis + pure-Go SQLite + a mock host agent that responds to `/containers/*` endpoints. No external services needed; runs in ~13 s, ~33 tests today. CI runs this on every PR.
- **`tests/e2e/`** — hits a running server (local or staging) through real HTTP/WebSocket. Requires the service to be up; CI does not run it.

The integration tests use a hand-rolled SQLite schema in [`tests/integration/sqlite.go`](tests/integration/sqlite.go) (Postgres advisory locks + `pq.StringArray` aren't SQLite-portable). A `assertSchemaMatchesModels` drift guard runs on every test setup and fails fast with a clear error if a new model column is missing from the SQLite schema — **always update `sqlite.go` when you add a column to a model**.

## Common commands

Run from the repo root unless noted.

| Task | Command |
|---|---|
| Local dev (Docker compose: app + Postgres + Redis) | `make up` / `make down` / `make logs` |
| Local with fresh build | `make fresh` |
| Connect to local Postgres / Redis | `make pg-local` / `make redis-local` |
| Connect to staging Postgres / Redis | `make pg-stg` / `make redis-stg` |
| Build & push the example game-server image | `make example-push` |
| Build & push the sidecar image | `make sidecar-push` |
| Regenerate swagger docs | `make swagger` (requires `swag` v1.16.6 in `$GOPATH/bin`) |
| Build everything | `go build ./...` |
| Integration tests (matches CI) | `go test -count=1 -timeout 180s ./tests/integration/...` |
| E2E test against staging | `go test -v ./tests/e2e -args -url https://elomm.net` |
| Single integration test | `go test -count=1 -run TestMatchPairingTwoGuests ./tests/integration/...` |

## Environment variables

[`src/server/config.go`](src/server/config.go) is the source of truth. **Required** at startup or the process panics: `FLY_API_HOSTNAME`, `FLY_API_KEY`, `FLY_APP_NAME`, `REDIS_URL`, `DATABASE_URL`, `HCLOUD_TOKEN`, `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_REGION`, `AWS_BUCKET_NAME`.

Loaded from `.env` then `config.env` via `godotenv`. All `*.env` files (including `staging.env` and `local.env`) are gitignored.

Notable tunables:

- `HCLOUD_HOST_TYPE` (default `cx33`) — validated against Hetzner's live catalog at startup so a deprecated type fails loudly instead of at first match.
- `HCLOUD_MAX_HOSTS` (default 5), `HCLOUD_MAX_SLOTS_PER_HOST` (default 8), `HCLOUD_WARM_SLOTS` (default 0; production = 1).
- `HCLOUD_AGENT_PORT` (default 8080), `HCLOUD_PORT_RANGE_START` / `_END` (default 7000-9000) — host-port pool the agent allocates from.
- `MATCHMAKING_INTERVAL` (default 100 ms), `MATCH_GC_INTERVAL` (default 1 m) — minimum spacing between worker passes.

## Domains

- `elomm.net` and `www.elomm.net` — public matchmaker (Cloudflare DNS → Fly app, Let's Encrypt cert via Fly).
- `gs.elomm.net` — reserved for per-game-server TLS hostnames (wildcard cert, used by the wildcard-TLS feature for WebGL clients connecting over `wss://`).

## Project conventions

**GORM nullable foreign keys must be pointer types.** A plain `string` defaults to `""` and gets stored as an empty string, not `NULL`, which breaks downstream `uuid` lookups (`SQLSTATE 22P02`). Use `*string` / `*uuid.UUID` and guard with `if x != nil && *x != ""` before dereferencing. Migrations adding such columns should also `UPDATE … SET col = NULL WHERE col = ''` to clean prior data. See [`.cursor/rules/gorm-nullable-fk.mdc`](.cursor/rules/gorm-nullable-fk.mdc).

**Query param casing.** The matchmaking WebSocket uses `gameID` (camelCase), not `game_id`. This bites people often enough that `test_match.py` calls it out explicitly.

**Match status codes.** Mutation endpoints distinguish 4xx flavors that callers can switch on:
- `403 Forbidden` — caller is not the game owner (`models.ErrNotGameOwner`).
- `409 Conflict` — uniqueness violation on create (game name, username, email).
- `400 Bad Request` — missing required fields, malformed input.
- `500` is reserved for actual internal errors.

**Pubsub Subscribe is async — confirm before relying on it.** `Client.Subscribe()` (both real Redis and miniredis) returns immediately; the SUBSCRIBE doesn't necessarily reach the server before the next line of code runs. Any publish in that window is silently dropped. The `Watch*` helpers in [`src/external/redis/`](src/external/redis/) wrap their `Subscribe` calls in a `PubSubNumSub` poll that blocks until the subscriber count includes us — use these (not raw `Client.Subscribe`) so callers don't have to think about it. Equally important: subscribe BEFORE writing client-visible state (e.g. `lobby_joined`, `queue_joined`) — otherwise the client can act on the welcome message and trigger a publish that races our own SUBSCRIBE. See [`openLobbySubs`](src/api/lobby/lobby.go) for the lobby flow's solution.

**Keep `docs/` in sync with feature changes.** [`docs/elo-service-client.md`](docs/elo-service-client.md) and [`docs/elo-service-server.md`](docs/elo-service-server.md) are the canonical client- and game-server-facing API references — not auto-generated, hand-maintained. Any change that adds, removes, or alters a route, payload field, status semantic, env var, or container contract must update the relevant doc *in the same PR*. The endpoint cheatsheet at the bottom of each is the part most often forgotten — check it. Stale docs mislead integrators and erode trust faster than a missing feature.

**S3 layout has three prefixes with different lifecycles.**

- `logs/` — post-match container stdout, written once on EndMatch by `saveMatchLogs`. Retained indefinitely (no lifecycle rule today).
- `live/<matchID>/<seq>.bin` + `manifest.json` — in-flight spectator chunks. Written by [`src/worker/spectator/uploader.go`](src/worker/spectator/uploader.go) at ~1s cadence while the match is `started`. Should never accumulate post-match — `MoveSpectateLiveToReplay` clears it on EndMatch. Orphans here mean the move failed; investigate or sweep manually.
- `replay/<matchID>/<seq>.bin` + `manifest.json` — finalized spectator stream. Manifest carries `finalized: true`. **Retained indefinitely** by deliberate choice — replays double as a permanent archive that game-replay tooling can build on. No lifecycle rule is configured. If storage cost becomes an issue at scale, the right knob is an S3 lifecycle rule on `replay/` (e.g. archive to Glacier after 90d, expire after a year), but don't add one without telling integrators — the indefinite retention is documented to spectator clients in [`docs/elo-service-client.md`](docs/elo-service-client.md).
- `artifacts/<matchID>/<name>` + `index.json` — game-server-uploaded named artifacts (replay files, preview images, etc., capped at 10 per match × 1 MiB each). Written via `POST /match/artifact` while the match is underway; index.json is rewritten on every upload via read-modify-write (concurrent uploads to the same match can race — game servers typically don't). Names appear on `MatchResult.Artifacts` after EndMatch reads the index and persists them. Retained alongside `MatchResult` rows — no separate retention policy. Bytes are opaque; the platform doesn't validate format.
