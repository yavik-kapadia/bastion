#!/usr/bin/env bash
# Usage: test-multi-stream.sh [N] [BITRATE]
# Publishes N simultaneous synthetic test streams to a running Bastion instance.
#
# Requirements: ffmpeg with libx264 and testsrc2 filter
# Defaults: N=3, BITRATE=1M, HOST=127.0.0.1, PORT=9710

set -euo pipefail

N="${1:-3}"
BITRATE="${2:-1M}"
HOST="${BASTION_HOST:-127.0.0.1}"
PORT="${BASTION_PORT:-9710}"

echo "Launching $N stream(s) at ${BITRATE}bps each to srt://${HOST}:${PORT}"

PIDS=()

for i in $(seq 1 "$N"); do
  STREAM_NAME="test-stream-${i}"
  SRT_URL="srt://${HOST}:${PORT}?streamid=#!::m=publish,r=${STREAM_NAME}"

  ffmpeg -hide_banner -loglevel error \
    -re \
    -f lavfi -i "testsrc2=size=1280x720:rate=30,drawtext=text='Stream ${i}':fontsize=64:x=50:y=50:fontcolor=white" \
    -f lavfi -i "sine=frequency=$((440 + i * 110)):sample_rate=48000" \
    -c:v libx264 -preset ultrafast -tune zerolatency -b:v "$BITRATE" \
    -c:a aac -b:a 128k \
    -f mpegts "$SRT_URL" &
  PIDS+=($!)
  echo "  Stream $i started (PID ${PIDS[-1]}): $STREAM_NAME"
done

echo ""
echo "All $N stream(s) running. Press Ctrl+C to stop."

# Forward signals to child processes.
cleanup() {
  echo ""
  echo "Stopping streams..."
  for pid in "${PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
}
trap cleanup EXIT INT TERM

wait
