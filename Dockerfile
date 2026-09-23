# ==============================================================================
# JobCon Dockerfile (Debian Multi-Stage Build)
# ==============================================================================

# --- Stage 1: Build static Go binary ---
FROM golang:trixie AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /bin/jobcon \
    ./cmd/jobcon

# --- Stage 2: Runtime Image (Debian Trixie Slim) ---
FROM debian:trixie-slim

# Install standard Linux / Debian utilities
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    openssh-client \
    curl \
    bash \
    && rm -rf /var/lib/apt/lists/*

# Create standard non-root user jobcon (UID 1000)
RUN groupadd -g 1000 jobcon && \
    useradd -u 1000 -g jobcon -m -s /bin/bash jobcon

WORKDIR /app

# Prepare application directories
RUN mkdir -p /app/data/logs /app/scripts /app/config && \
    chown -R jobcon:jobcon /app

# Copy binary and default files
COPY --from=builder --chown=jobcon:jobcon /bin/jobcon /app/jobcon
COPY --chown=jobcon:jobcon scripts/ /app/scripts/
COPY --chown=jobcon:jobcon config.example.yaml /app/config.example.yaml

USER jobcon

# Environment defaults for container execution
ENV JOBCON_BIND=0.0.0.0 \
    JOBCON_PORT=8080 \
    JOBCON_DB_PATH=/app/data/jobcon.db \
    JOBCON_LOGS_DIR=/app/data/logs \
    JOBCON_SCRIPTS_DIR=/app/scripts

EXPOSE 8080

VOLUME ["/app/data", "/app/scripts"]

ENTRYPOINT ["/app/jobcon"]
CMD ["--config", "/app/config/config.yaml"]
