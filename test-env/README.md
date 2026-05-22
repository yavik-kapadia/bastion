# test-env

End-to-end smoke test environment for Bastion using Docker Compose.

## What it spins up

| Service     | Purpose                                                              |
|-------------|----------------------------------------------------------------------|
| `bastion`   | The published image `yavik/bastion:latest` on the standard ports.    |
| `init`      | One-shot; bootstraps the admin user via `POST /api/v1/auth/bootstrap`. |
| `publisher` | ffmpeg sending an MPEG-TS testsrc into Bastion as stream `test`.     |
| `viewer`    | ffmpeg subscribing to `test` and discarding bytes (headless sink).   |

## Run

```bash
docker compose -f docker-compose.test.yml up
```

Dashboard: <http://localhost:8080>
Login: `admin` / `test-admin-password` (set via env in the compose file).

## Common verification

```bash
# Live relay state (active publishers / subscribers, bytes relayed). This is
# the authoritative source for ad-hoc streams; the /api/v1/streams REST
# endpoint only lists DB-registered streams.
curl -s http://localhost:8080/metrics | grep -E '^bastion_(active_publishers|active_subscribers|bytes_relayed_total)'

# Scale viewers to confirm bastion_active_subscribers tracks reality.
docker compose -f docker-compose.test.yml up -d --scale viewer=4

# Reproduce a publisher EOF/reconnect cycle (the v0.2.4 freeze scenario).
docker compose -f docker-compose.test.yml restart publisher

# Wipe state and start fresh.
docker compose -f docker-compose.test.yml down -v
```

### Authenticated REST calls

Login returns a Bearer token; pass it via `Authorization: Bearer <token>`:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"test-admin-password"}' | jq -r .data.token)
curl -s -H "Authorization: Bearer $TOKEN" http://localhost:8080/api/v1/streams | jq .
```

## Visual playback

The bundled `viewer` is headless on purpose so it works on any host. For
actual video playback, run ffplay on your **host** machine:

```bash
ffplay 'srt://localhost:9710?streamid=#!::m=request,r=test'
```

Quote with single quotes so zsh leaves `#`, `!`, and `:` literal.

### ffplay must be built with libsrt

The mainline `homebrew-core/ffmpeg` formula **does not include libsrt** — you
get `Protocol not found` if you try to dial an `srt://` URL with it.

Install an SRT-enabled build from the community tap (one-time, ~5–10min source build):

```bash
brew uninstall ffmpeg          # remove the no-srt version if present
brew tap homebrew-ffmpeg/ffmpeg
brew install homebrew-ffmpeg/ffmpeg/ffmpeg --with-srt
```

Verify with `ffmpeg -protocols 2>&1 | grep ^srt$` (expect a match) or look
for `--enable-libsrt` in `ffplay -version`'s `configuration:` line.

### VLC

VLC's URL parser treats `#` as a fragment separator and silently drops
everything after it, so the standard SRT URL won't work pasted directly into
"Open Network Stream." URL-encode it as `%23`:

```
srt://localhost:9710?streamid=%23!::m=request,r=test
```

If that still misbehaves on your VLC build (a few do), prefer `ffplay`.

## Notes

- `bastion.test.toml` has `allow_unregistered_streams = true`, so the publisher
  is accepted without pre-creating the `test` stream in the database.
- The DB lives in a named volume `bastion-test-data`. Use `down -v` to wipe.
- This image is `yavik/bastion:latest`. To test a local build, change the
  `bastion` service to `build: .` instead of `image:`.
