# Multi-Stage Build for Production app-engine
# Stage 1: Build stage
FROM golang:alpine AS builder

WORKDIR /src
ENV GOTOOLCHAIN=auto
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Compile static, stripped binary with zero external cgo dependencies
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -extldflags '-static'" \
    -o /app/app-engine ./cmd/server

# Stage 2: Minimal, HIPAA-hardened runtime stage
FROM alpine:latest

# Install security updates, certificates, and utilities
RUN apk update && \
    apk upgrade --no-cache && \
    apk add --no-cache ca-certificates tzdata curl && \
    rm -rf /var/cache/apk/*

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/app-engine /app/app-engine

# HIPAA Compliance Standard: Run as non-privileged service user
USER 10001:10001

EXPOSE 8080

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:8080/livez || exit 1

ENTRYPOINT ["/app/app-engine"]
