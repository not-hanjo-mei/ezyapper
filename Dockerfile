# EZyapper - Multi-stage Docker Build
# Produces minimal size binary image
# Note: Qdrant runs as a separate service

# Build stage
FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build binary (CGO not needed - pure Go)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s" \
    -o /build/ezyapper \
    ./cmd/bot

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN adduser -D -g '' appuser

# Copy binary from builder
COPY --from=builder /build/ezyapper /app/ezyapper
COPY --from=builder /build/config.yaml /app/config.yaml
COPY --from=builder /build/web /app/web

# Create logs directory
RUN mkdir -p /app/logs && \
    chown -R appuser:appuser /app

# Switch to non-root user
USER appuser

# Expose WebUI port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Set entrypoint
ENTRYPOINT ["/app/ezyapper"]
CMD ["-config", "/app/config.yaml"]
