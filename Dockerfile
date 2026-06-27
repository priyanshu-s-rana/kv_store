# ---- Stage 1: builder ----
FROM golang:1.25-alpine AS builder

WORKDIR /build

# Cache dependencies separately
COPY go.mod go.sum* ./
RUN go mod download

# Copy source
COPY . .

# Build binaries
RUN mkdir -p /build/bin && \
    CGO_ENABLED=0 GOOS=linux go build -o /build/bin/kv-server ./cmd/kv-server && \
    CGO_ENABLED=0 GOOS=linux go build -o /build/bin/kv-cli ./cmd/kv-cli

# ---- Stage 2: runtime ----
FROM alpine:3.20

# Useful for HEALTHCHECK using nc
RUN apk add --no-cache netcat-openbsd

WORKDIR /app

# Create required directories
RUN mkdir -p /app/config /data

# Copy binaries
COPY --from=builder /build/bin/kv-server /build/bin/kv-cli /app/

# Copy config
COPY config/config.dev.yaml config/config.prod.yaml /app/config/

# Non-root user, with permission fixed with every copy above
RUN addgroup -S kvstore && adduser -S kvstore -G kvstore && \
    chown -R kvstore:kvstore /app /data

USER kvstore

EXPOSE 5040

# Health check using your RESP PING command
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD echo "PING" | nc localhost 5040 | grep PONG || exit 1

ENTRYPOINT ["/app/kv-server"]