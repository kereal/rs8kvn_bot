# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Install build dependencies and UPX for binary compression
RUN apk add --no-cache gcc musl-dev upx

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Ensure go.mod is consistent with dependencies
RUN go mod tidy

# Build with optimizations:
# - trimpath: remove file system paths for reproducible builds
# - ldflags: strip debug info (-s) and DWARF (-w)
RUN CGO_ENABLED=1 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=prod" \
    -o rs8kvn_bot ./cmd/bot

# Compress binary with UPX (reduces size by ~30-40%)
RUN upx -9 rs8kvn_bot

# Runtime stage - using minimal alpine
FROM alpine:3.23

# Install only essential runtime dependencies
RUN apk add --no-cache ca-certificates procps && \
    rm -rf /var/cache/apk/*

# Create non-root user for security
RUN adduser -D -g '' appuser

# Create app directory and data directory with proper permissions
WORKDIR /app
RUN mkdir -p /app/data && chown -R appuser:appuser /app/data

# Copy binary from builder
COPY --from=builder --chown=appuser:appuser /app/rs8kvn_bot .

# Switch to non-root user
USER appuser

# Memory optimization environment variables:
# GOMEMLIMIT: soft memory limit for GC (64MB in bytes)
# GOGC: more aggressive GC (default 100, lower = more aggressive)
ENV GOMEMLIMIT=67108864
ENV GOGC=50

# Expose nothing (bot uses polling)
EXPOSE 0

# Health check - verifies process is running
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD pgrep rs8kvn_bot > /dev/null && exit 0 || exit 1

# Run the bot
CMD ["/app/rs8kvn_bot"]
