# Example Game Server

A Go application that demonstrates how to create a dockerized game server that listens for HTTP and TCP requests to collect player IDs from a predefined list, waits for all expected players to join, then simulates a game and reports results to the ELO service.

## Features

- **HTTP Server**: Accepts POST requests with player IDs
- **TCP Server**: Accepts text-based player ID registration
- **Predefined Players**: Only allows players specified as command line arguments to join
- **Player Collection**: Waits for all expected players to join before starting
- **Game Simulation**: Randomly selects a winner from the collected players
- **Result Reporting**: Automatically reports game results to the ELO service
- **Health Check**: Provides `/health` endpoint for monitoring server status
- **Auto Shutdown**: Server automatically shuts down after reporting results

## Usage

### Building the Docker Image

```bash
docker build -t example-game-server .
```

### Running the Container

```bash
# Run with 2 players (alice and bob)
docker run -p 8080:8080 -p 8081:8081 example-game-server -token your-token-id alice bob

# Run with 3 players and custom ports
docker run -p 9000:9000 -p 9001:9001 example-game-server \
  -token your-token-id \
  -http-port 9000 \
  -tcp-port 9001 \
  alice bob charlie

# Run with 4 players
docker run -p 8080:8080 -p 8081:8081 example-game-server -token your-token-id player1 player2 player3 player4
```

### Command Line Options

- `-token`: Token ID (required)
- `-http-port`: HTTP server port (default: 8080)
- `-tcp-port`: TCP server port (default: 8081)
- `player1 player2 ...`: Expected player IDs (required, at least one)

## API Endpoints

### HTTP API

#### Player Join Endpoint

**Endpoint**: `POST /join`

**Request Body**: Plain text containing the player ID

**Response**: Status message indicating join success and current player count

**Example using curl**:
```bash
# Start server with expected players: alice, bob
docker run -p 8080:8080 -p 8081:8081 example-game-server -token test123 alice bob

# Alice joins
curl -X POST http://localhost:8080/join \
  -H "Content-Type: text/plain" \
  -d "alice"

# Bob joins
curl -X POST http://localhost:8080/join \
  -H "Content-Type: text/plain" \
  -d "bob"

# Charlie tries to join (will be rejected)
curl -X POST http://localhost:8080/join \
  -H "Content-Type: text/plain" \
  -d "charlie"
```

**Example Response**:
```
Player alice joined successfully. Players: 1/2
Player bob joined successfully. Players: 2/2
```

#### Health Check Endpoint

**Endpoint**: `GET /health`

**Response**: JSON object with server status and player information

**Example using curl**:
```bash
curl http://localhost:8080/health
```

**Example Response**:
```json
{
  "status": "healthy",
  "token_id": "test123",
  "expected_players": ["alice", "bob"],
  "joined_players": ["alice"],
  "player_count": 1,
  "expected_count": 2,
  "ready": false
}
```

**Health Check Response Fields**:
- `status`: Always "healthy" when server is running
- `token_id`: The game token ID
- `expected_players`: List of all expected player IDs
- `joined_players`: List of currently joined player IDs
- `player_count`: Number of players who have joined
- `expected_count`: Total number of expected players
- `ready`: Boolean indicating if all players have joined

### TCP API

**Command Format**: Send player ID as plain text, terminated with newline

**Response Format**: Status message indicating join success and current player count

**Example using netcat**:
```bash
# Start server with expected players: alice, bob
docker run -p 8080:8080 -p 8081:8081 example-game-server -token test123 alice bob

# Alice joins
echo "alice" | nc localhost 8081

# Bob joins
echo "bob" | nc localhost 8081

# Charlie tries to join (will be rejected)
echo "charlie" | nc localhost 8081
```

**Expected Response**:
```
OK: Player alice joined successfully. Players: 1/2
OK: Player bob joined successfully. Players: 2/2
ERROR: Player not expected in this game
```

## How it Works

1. **Server Startup**: The server starts with a predefined list of expected player IDs
2. **Player Registration**: Only players from the expected list can join via HTTP POST to `/join` or TCP connection
3. **Player Collection**: The server tracks which expected players have joined
4. **Health Monitoring**: The `/health` endpoint provides real-time status of player join progress
5. **Game Trigger**: Once all expected players have joined, the game automatically starts
6. **Game Simulation**: A winner is randomly selected from the players
7. **Result Reporting**: The result is sent to the ELO service
8. **Server Shutdown**: The server automatically shuts down after reporting results

## Environment Variables

- `WEBSITE_URL`: URL for the ELO service result reporting endpoint (defaults to `https://elo-service.fly.dev/result/report`)

## Error Handling

### HTTP API Errors
- `400 Bad Request`: Missing or empty player ID
- `403 Forbidden`: Player ID not in the expected list
- `405 Method Not Allowed`: Non-POST requests
- `409 Conflict`: Player already joined

### TCP API Errors
- `ERROR: Player ID is required`: Empty or missing player ID
- `ERROR: Player not expected in this game`: Player ID not in the expected list
- `ERROR: Player already joined`: Player already registered

## Docker Compose Example

```yaml
version: '3.8'
services:
  game-server:
    build: .
    ports:
      - "8080:8080"
      - "8081:8081"
    environment:
      - WEBSITE_URL=https://your-elo-service.com/result/report
    command: ["-token", "your-token-id", "player1", "player2", "player3"]
```

## Testing

### Test HTTP API
```bash
# Start the server with expected players
docker run -p 8080:8080 -p 8081:8081 example-game-server -token test123 alice bob

# In another terminal, test HTTP
curl -X POST http://localhost:8080/join -H "Content-Type: text/plain" -d "alice"
curl -X POST http://localhost:8080/join -H "Content-Type: text/plain" -d "bob"
```

### Test TCP API
```bash
# Start the server with expected players
docker run -p 8080:8080 -p 8081:8081 example-game-server -token test123 alice bob

# In another terminal, test TCP
echo "alice" | nc localhost 8081
echo "bob" | nc localhost 8081
```

## Server Logs

The server provides detailed logging:

```
Starting example game server:
  Token ID: test123
  Expected players: [alice bob]
  HTTP port: 8080
  TCP port: 8081
HTTP server listening on port 8080
TCP server listening on port 8081
Player alice joined. Total players: 1/2
Player bob joined. Total players: 2/2
All players have joined! Starting game...
Simulating game with 2 players: [alice bob]
Game finished! Winner: bob
Sending result to https://elo-service.fly.dev/result/report
Result sent successfully. Status: 200 OK, Body: {"success":true}
Shutting down server...
Shutting down TCP server...
TCP server shutdown complete
Shutting down HTTP server...
Server shutdown complete
```

## Health Check Usage

The health check endpoint is useful for:

- **Monitoring**: Check if the server is running and healthy
- **Progress Tracking**: See how many players have joined vs. expected
- **Readiness**: Determine if the game is ready to start (all players joined)
- **Debugging**: View current server state and configuration

**Example monitoring script**:
```bash
# Check server health every 5 seconds
while true; do
  curl -s http://localhost:8080/health | jq .
  sleep 5
done
``` 