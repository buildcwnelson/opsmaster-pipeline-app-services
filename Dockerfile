# Forwarding Dockerfile for OpsMaster app-engine
FROM golang:alpine AS builder

WORKDIR /src
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache git ca-certificates

COPY app-services/go.mod app-services/go.sum ./
RUN go mod download

COPY app-services/ .

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/app-engine ./cmd/server

FROM alpine:latest

RUN apk update && \
    apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata curl && \
    rm -rf /var/cache/apk/*

WORKDIR /app
COPY --from=builder /app/app-engine /app/app-engine

USER 10001:10001
EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/livez || exit 1

ENTRYPOINT ["/app/app-engine"]
