# Bastion SRT Relay — Standard Operating Procedures

## Table of Contents
1. [Build & Run](#1-build--run)
2. [Configuration Reference](#2-configuration-reference)
3. [Stream Management](#3-stream-management)
4. [User & API Key Management](#4-user--api-key-management)
5. [Encryption Setup](#5-encryption-setup)
6. [Monitoring & Metrics](#6-monitoring--metrics)
7. [Multi-Stream Testing](#7-multi-stream-testing)
8. [Docker Deployment](#8-docker-deployment)
9. [Troubleshooting](#9-troubleshooting)

---

## 1. Build & Run

### Prerequisites
- Go 1.22+
- Node.js 20+ (for frontend)
- ffmpeg (optional, for manual stream testing)

### Build

```bash
# Build backend only
go build -o bastion ./cmd/bastion

# Build frontend then backend (produces single embedded binary)
cd frontend && npm ci && npm run build && cd ..
go build -o bastion ./cmd/bastion
```

### Run

```bash
# Development (uses bastion.toml in current directory)
./bastion

# Specify config file
./bastion --config /etc/bastion/bastion.toml

# Print version
./bastion --version
```

### Stop

Send `SIGINT` (Ctrl+C) or `SIGTERM`. Bastion drains active streams gracefully before exiting.

---

## 2. Configuration Reference

Configuration is read from `bastion.toml`. See the inline comments in the default config for all options.

| Section | Key | Default | Description |
|---------|-----|---------|-------------|
| `[srt]` | `listen_addr` | `:9710` | UDP address for SRT connections |
| `[srt]` | `latency` | `120ms` | End-to-end buffering latency |
| `[srt]` | `payload_size` | `1316` | Packet payload size (7 MPEG-TS packets) |
| `[srt]` | `subscriber_buf_size` | `512` | Per-subscriber ring-buffer packet count |
| `[srt]` | `allow_unregistered_streams` | `true` | Allow streams not pre-configured in DB |
| `[api]` | `listen_addr` | `:8080` | HTTP API + dashboard address |
| `[api]` | `jwt_secret` | — | **Change in production** |
| `[api]` | `encryption_key` | — | AES-256 key for at-rest passphrase encryption |
| `[database]` | `path` | `./bastion.db` | SQLite file path |
| `[logging]` | `level` | `info` | `debug`, `info`, `warn`, `error` |
| `[logging]` | `format` | `text` | `text` or `json` |

---

## 3. Stream Management

### Via Dashboard
Navigate to `http://<host>:8080` → **Streams** → **New Stream**.

### Via REST API

```bash
BASE=http://localhost:8080
KEY=<your-api-key>

# List all configured streams
curl -H "Authorization: Bearer $KEY" $BASE/api/v1/streams

# Create a stream
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  $BASE/api/v1/streams \
  -d '{
    "name": "my-stream",
    "description": "Live event",
    "key_length": 32,
    "passphrase": "at-least-10-chars",
    "max_subscribers": 100
  }'

# Update a stream
curl -X PUT -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  $BASE/api/v1/streams/my-stream \
  -d '{"max_subscribers": 200}'

# Delete a stream
curl -X DELETE -H "Authorization: Bearer $KEY" $BASE/api/v1/streams/my-stream
```

### Stream ID Format

Bastion supports two formats for the SRT stream ID:

**Modern (recommended):**
```
#!::m=publish,r=<stream-name>
#!::m=request,r=<stream-name>
```

**Legacy (compatibility):**
```
publish/<stream-name>
request/<stream-name>
```

### Publishing with ffmpeg

```bash
ffmpeg -re -i input.ts -c copy -f mpegts \
  "srt://localhost:9710?streamid=#!::m=publish,r=my-stream"
```

### Subscribing with ffplay

```bash
ffplay "srt://localhost:9710?streamid=#!::m=request,r=my-stream"
```

---

## 4. User & API Key Management

### Initial Setup

On first run, Bastion creates a default admin user:
- **Username:** `admin`
- **Password:** printed to stdout on first boot — change immediately.

### Create a User

```bash
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  $BASE/api/v1/users \
  -d '{"username": "operator1", "password": "secure-password", "role": "operator"}'
```

Roles: `admin` (full access), `operator` (stream CRUD), `viewer` (read-only).

### Create an API Key

```bash
curl -X POST -H "Authorization: Bearer $KEY" \
  $BASE/api/v1/auth/api-keys \
  -d '{"name": "ci-bot"}'
# Returns the raw key — store it securely, it is shown only once.
```

---

## 5. Encryption Setup

Bastion supports per-stream AES-128, AES-192, or AES-256 encryption via SRT's built-in passphrase mechanism.

### Create an Encrypted Stream

```bash
curl -X POST -H "Authorization: Bearer $KEY" \
  -H "Content-Type: application/json" \
  $BASE/api/v1/streams \
  -d '{
    "name": "secure-stream",
    "passphrase": "minimum-ten-chars",
    "key_length": 32
  }'
```

`key_length` values: `0` = no encryption, `16` = AES-128, `24` = AES-192, `32` = AES-256.

### Publish to Encrypted Stream

```bash
ffmpeg -re -i input.ts -c copy -f mpegts \
  "srt://localhost:9710?streamid=#!::m=publish,r=secure-stream&passphrase=minimum-ten-chars&pbkeylen=32"
```

### Subscribe to Encrypted Stream

```bash
ffplay "srt://localhost:9710?streamid=#!::m=request,r=secure-stream&passphrase=minimum-ten-chars"
```

Connections without the correct passphrase are rejected.

---

## 6. Monitoring & Metrics

### Prometheus

Prometheus metrics are exposed at `http://localhost:8080/metrics`.

Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `bastion_active_streams` | Gauge | Number of streams with an active publisher |
| `bastion_active_subscribers` | Gauge | Total subscriber connections |
| `bastion_bytes_relayed_total` | Counter | Bytes relayed per stream |
| `bastion_packet_loss_rate` | Gauge | Packet loss % per stream |
| `bastion_rtt_ms` | Gauge | Round-trip time per connection |

### Real-Time Dashboard

WebSocket stream at `ws://localhost:8080/api/v1/ws` pushes JSON metric snapshots every second. The web dashboard at `http://localhost:8080` consumes this automatically.

### Grafana

With docker-compose, Grafana is available at `http://localhost:3000`. Import the pre-built dashboard from `deploy/grafana/`.

---

## 7. Multi-Stream Testing

### Launch Multiple Test Streams

```bash
# Publish N simultaneous test streams using ffmpeg test sources
bash scripts/test-multi-stream.sh 5

# Custom stream count and bitrate
N=10 BITRATE=2M bash scripts/test-multi-stream.sh $N
```

### Go Integration Tests

```bash
# Run all tests
go test ./... -v

# Run integration tests only
go test ./test/... -v -count=1

# Run multi-stream test (spins up real SRT connections in-process)
go test ./test/ -run TestMultiStream -v
```

---

## 8. Docker Deployment

```bash
# Build and start full stack (Bastion + Prometheus + Grafana)
docker compose up --build

# Production: override config via volume
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# View logs
docker compose logs -f bastion
```

Ports:
- `9710/udp` — SRT relay
- `8080/tcp` — API + dashboard
- `9090/tcp` — Prometheus (if using docker-compose)
- `3000/tcp` — Grafana (if using docker-compose)

---

## 9. Troubleshooting

### Publisher connects but subscribers receive nothing

1. Check stream name matches exactly (case-sensitive).
2. Verify `allow_unregistered_streams = true` or the stream is created in the DB.
3. Check `bastion` logs at `debug` level: `level = "debug"` in `bastion.toml`.

### "Rejected" error on SRT connection

- Stream requires encryption but client sent no passphrase → add `&passphrase=...` to the SRT URL.
- IP not in `allowed_publishers` list → update the stream config.
- `max_subscribers` limit reached → increase the limit or wait for a slot.

### High packet loss

- Increase `latency` in `[srt]` config (try 200ms, 300ms, 500ms for high-latency links).
- Check network path MTU; SRT default payload size of 1316 bytes is safe for most networks.

### Database locked / WAL errors

- Only one Bastion process should own the SQLite file.
- Ensure the DB directory is writable by the bastion process user.
