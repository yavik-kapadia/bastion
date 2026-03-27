# ── Stage 1: Build frontend ────────────────────────────────────────────────────
FROM node:22-alpine AS frontend-builder

WORKDIR /app/frontend
COPY frontend/package*.json ./
RUN npm ci

COPY frontend/ .
RUN npm run build

# ── Stage 2: Build Go binary ───────────────────────────────────────────────────
FROM golang:1.26-alpine AS go-builder

WORKDIR /app

# Copy dependency files first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy source.
COPY . .

# Copy built frontend assets into the embed path.
COPY --from=frontend-builder /app/frontend/build ./cmd/bastion/frontend/

# Build a fully static binary (no CGo, no external libs).
RUN CGO_ENABLED=0 GOOS=linux go build -a -ldflags="-w -s" -o bastion ./cmd/bastion

# ── Stage 3: Runtime image ─────────────────────────────────────────────────────
FROM alpine:3.20

# Install CA certificates, timezone data, and ffmpeg for stream thumbnail generation.
RUN apk add --no-cache ca-certificates tzdata ffmpeg

WORKDIR /bastion

COPY --from=go-builder /app/bastion ./bastion
COPY bastion.toml ./bastion.toml

# Create data directory for SQLite database.
RUN mkdir -p /data

# Expose SRT (UDP) and HTTP API.
EXPOSE 9710/udp
EXPOSE 8080/tcp

ENTRYPOINT ["./bastion"]
CMD ["--config", "bastion.toml"]
