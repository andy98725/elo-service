# Example Server

A simple Go application that demonstrates how to create a dockerized server that takes a token ID and player IDs as arguments, waits 5 seconds, then sends a request to a specified website.

## Usage

### Building the Docker Image

#### With default website (example.com):
```bash
docker build -t example-server .
```

#### With custom website:
```bash
docker build --build-arg WEBSITE_URL=https://your-api.com -t example-server .
```

### Running the Container

#### Using the default website (set during build):
```bash
docker run example-server -token <TOKEN_ID> <PLAYER_ID_1> <PLAYER_ID_2> ...
```

#### Override website at runtime:
```bash
docker run example-server -token <TOKEN_ID> -website https://your-api.com <PLAYER_ID_1> <PLAYER_ID_2> ...
```

### Examples

```bash
# Build with custom website
docker build --build-arg WEBSITE_URL=https://api.mygame.com -t example-server .

# Run with default website (from build arg)
docker run example-server -token "abc123" "player1" "player2" "player3"

# Run with runtime website override
docker run example-server -token "abc123" -website https://api.mygame.com "player1" "player2" "player3"
```

## What it does

1. Takes a token ID (required) and variable number of player IDs as arguments
2. Waits 5 seconds
3. Randomly selects one of the provided player IDs
4. Sends a POST request to the specified website with the following JSON body:
   ```json
   {
     "token_id": "<TOKEN_ID>",
     "player_id": "<RANDOM_PLAYER_ID>"
   }
   ```

## Arguments

- `-token`: Token ID (required)
- `-website`: Website URL to send request to (optional, defaults to build-time value or example.com)
- Additional arguments: Player IDs (at least one required)

## Build Arguments

- `WEBSITE_URL`: Default website URL to use (defaults to https://example.com)

## Output

The application will log:
- The token and player IDs it received
- The target website URL
- A 5-second wait message
- The request being sent
- The response status from the website 