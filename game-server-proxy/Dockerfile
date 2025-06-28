FROM golang:1.21-alpine AS builder

WORKDIR /app

# Copy go mod files
COPY game-server-proxy/go.mod game-server-proxy/go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY game-server-proxy/ .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o game-server-proxy .

# Final stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder stage
COPY --from=builder /app/game-server-proxy .

# Expose ports
EXPOSE 8082 8083

# Run the application
CMD ["./game-server-proxy"] 