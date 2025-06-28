# Build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o example-game-server .

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates
WORKDIR /root/

COPY --from=builder /app/example-game-server .
RUN chmod +x example-game-server

ARG WEBSITE_URL=https://elo-service.fly.dev/result/report
ENV WEBSITE_URL=$WEBSITE_URL

ENTRYPOINT ["./example-game-server"] 