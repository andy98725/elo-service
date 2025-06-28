# Game Server Proxy

This is a reverse proxy server that forwards both HTTP and TCP connections to Fly.io machines within the same app.

## Overview

The proxy server runs as part of the `elo-service` Fly.io app and provides two main services:

1. **HTTP Proxy**: Forwards HTTP requests to specific game server machines
2. **TCP Proxy**: Forwards raw TCP connections to specific game server machines

## Architecture

The proxy forwards connections to Fly.io machines using the internal DNS format:
`{machineID}.vm.{app-name}.internal:{port}`

## HTTP Proxy Usage

The HTTP proxy expects requests in the format:
```
/{machineID}/{port}/{rest-of-path}
```

### Example:
- Request: `GET /abc123/8080/api/status`
- Forwards to: `http://abc123.vm.elo-service.internal:8080/api/status`

## TCP Proxy Usage

The TCP proxy expects connections to send initial data in the format:
```
{machineID}:{port}
```

### Example:
- Connect to proxy on port 8081
- Send initial data: `abc123:8080`
- Proxy forwards all subsequent data to `abc123.vm.elo-service.internal:8080`

## Configuration

The proxy is configured via environment variables:

- `FLY_APP_NAME`: The Fly.io app name (default: "elo-service")
- `HTTP_PORT`: Port for HTTP proxy (default: 8080)
- `TCP_PORT`: Port for TCP proxy (default: 8081)

## Deployment

The proxy is deployed as part of the main `elo-service` app using Fly.io processes:

```bash
# Deploy the entire app (including proxy)
fly deploy

# Deploy only the proxy
fly deploy --process-group proxy
```

## Local Development

To run the proxy locally:

```bash
cd game-server-proxy
go mod download
go run main.go
```

## Integration with Matchmaking

This proxy integrates with the matchmaking system by:

1. When a match is created, the matchmaking service spawns a game server machine
2. The proxy receives the machine ID and port information
3. Players connect to the proxy using the machine ID and port
4. The proxy forwards all traffic to the appropriate game server machine

## Security

- The proxy only forwards to machines within the same Fly.io app
- Internal communication uses Fly.io's private network
- External access is controlled by Fly.io's networking rules 