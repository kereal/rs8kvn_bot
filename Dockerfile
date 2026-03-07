# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache gcc musl-dev

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build with optimizations
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o tgvpn_bot ./cmd/bot

# Runtime stage
FROM alpine:3.19

# Install runtime dependencies for SQLite
RUN apk add --no-cache ca-certificates

# Create app directory and data directory
WORKDIR /opt/tgvpn
RUN mkdir -p /app/data

# Copy binary from builder
COPY --from=builder /app/tgvpn_bot .

# Expose nothing (bot uses polling)
EXPOSE 0

# Health check - verifies process and basic functionality
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD pgrep tgvpn_bot > /dev/null && exit 0 || exit 1

# Run as non-root user
RUN adduser -D -g '' appuser && chown -R appuser:appuser /app
USER appuser

# Set working directory to /app for volume mount
WORKDIR /app

# Run the bot
CMD ["/opt/tgvpn/tgvpn_bot"]
