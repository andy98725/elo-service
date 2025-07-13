# Game Server Sidecar

A minimal HTTP server that serves the contents of `/logs/server.log` on any query.

## Features

- Serves log file contents on any HTTP request
- Configurable port via `LOGS_PORT` environment variable
- Minimal footprint using Alpine Linux
- Returns plain text content type

## Usage

### Environment Variables

- `LOGS_PORT`: Port to listen on (default: 8080)

### Docker

Build the image:
```bash
docker build -t game-server-sidecar .
```

Run the container:
```bash
docker run -p 8080:8080 -v /path/to/logs:/logs game-server-sidecar
```

Or with custom port:
```bash
docker run -p 9000:9000 -e LOGS_PORT=9000 -v /path/to/logs:/logs game-server-sidecar
```

### Local Development

Run locally:
```bash
go run main.go
```

Or build and run:
```bash
go build -o main .
./main
```

## API

- **GET /** - Returns the contents of `/logs/server.log`
- Any other path will also return the same log contents

## Response

Returns the raw contents of the log file as plain text with `Content-Type: text/plain`. 