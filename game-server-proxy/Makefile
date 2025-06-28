.PHONY: build run clean deploy

# Build the proxy server
build:
	go build -o game-server-proxy .

# Run the proxy server locally
run: build
	./game-server-proxy

# Run the proxy server directly with go run
dev:
	go run main.go


# Clean build artifacts
clean:
	rm -f game-server-proxy

# Deploy to Fly.io
deploy:
	fly deploy --process-group proxy

# Deploy the entire app (including proxy)
deploy-all:
	fly deploy

# Show logs for the proxy
logs:
	fly logs --process-group proxy

# Scale the proxy
scale:
	fly scale count 1 --process-group proxy 