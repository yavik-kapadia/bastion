# Bastion

An SRT relay server with a real-time web dashboard. Publish one stream, relay to many subscribers — with per-stream encryption, access control, and live metrics.

## Features

- **SRT relay** — fan-out from one publisher to many subscribers
- **Web dashboard** — CRUD streams, view live metrics, manage users
- **Per-stream encryption** — AES-128/192/256 via SRT passphrases
- **Access control** — IP/CIDR allowlists, max subscriber limits, role-based API auth
- **Real-time metrics** — bitrate, RTT, packet loss, health indicators via WebSocket
- **Prometheus** — `/metrics` endpoint compatible with Grafana
- **Single binary** — frontend embedded; no external runtime dependencies

## Quick Start

```bash
# Build
go build -o bastion ./cmd/bastion

# Run with default config
./bastion

# Open dashboard
open http://localhost:8080
```

## Publishing a Stream

```bash
ffmpeg -re -i input.ts -c copy -f mpegts \
  "srt://localhost:9710?streamid=#!::m=publish,r=my-stream"
```

## Subscribing

```bash
ffplay "srt://localhost:9710?streamid=#!::m=request,r=my-stream"
```

## Docker

```bash
docker compose up --build
```

## Documentation

See [docs/SOP.md](docs/SOP.md) for full operating procedures.

## Configuration

Copy and edit `bastion.toml`:

```toml
[srt]
listen_addr = ":9710"
latency = "120ms"

[api]
listen_addr = ":8080"
jwt_secret = "change-me"

[database]
path = "./bastion.db"
```

## License

MIT
