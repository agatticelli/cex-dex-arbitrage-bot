# Multi-stage build for optimized image size

# Build stage
FROM golang:alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make

# Set working directory
WORKDIR /build

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the detector binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o detector \
    cmd/detector/main.go

# Runtime stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 arbitrage && \
    adduser -D -u 1000 -G arbitrage arbitrage

# Set working directory
WORKDIR /app

# Copy binary from builder
COPY --from=builder /build/detector .

# Copy config template (will be overridden by volume mount)
COPY config.example.yaml ./config.yaml

# Change ownership
RUN chown -R arbitrage:arbitrage /app

# Switch to non-root user
USER arbitrage

# Expose metrics and health check port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget --quiet --tries=1 --spider http://localhost:8080/health || exit 1

# Run the detector
CMD ["./detector"]
