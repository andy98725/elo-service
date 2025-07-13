# Build stage
FROM golang:1.23.1-alpine AS builder

WORKDIR /app
RUN apk add --no-cache gcc musl-dev


# Build main app
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o main ./src/main.go


# Run stage
FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ca-certificates

COPY --from=builder /app/main .
COPY config.env .

EXPOSE 8080 8081

CMD ["./main"]