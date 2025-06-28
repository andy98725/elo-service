# Build stage
FROM golang:1.23.1-alpine AS builder

WORKDIR /app
RUN apk add --no-cache gcc musl-dev

# Build main app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o main ./src/main.go

# Build proxy
WORKDIR /app/game-server-proxy
COPY game-server-proxy/go.mod game-server-proxy/go.sum ./
RUN go mod download
COPY game-server-proxy/ .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o game-server-proxy .

# Run stage
FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ca-certificates

# Copy both binaries
COPY --from=builder /app/main .
COPY --from=builder /app/game-server-proxy/game-server-proxy ./game-server-proxy
COPY config.env .

EXPOSE 8080 8081