#!/bin/sh
# init.sh — wait for the Bastion API to come up, bootstrap an admin user,
# then exit. Idempotent: a re-run against an already-bootstrapped instance
# is a no-op.
#
# Container: alpine + curl + jq.

set -eu

API="${BASTION_API:-http://bastion:8080}"
USERNAME="${BASTION_ADMIN_USERNAME:-admin}"
PASSWORD="${BASTION_ADMIN_PASSWORD:-test-admin-password}"

echo "init: waiting for Bastion API at $API ..."
for i in $(seq 1 60); do
  if curl -fsS "$API/health" >/dev/null 2>&1; then
    echo "init: API ready (took ${i}s)"
    break
  fi
  sleep 1
done

if ! curl -fsS "$API/health" >/dev/null 2>&1; then
  echo "init: ERROR API never came up" >&2
  exit 1
fi

STATUS_JSON=$(curl -fsS "$API/api/v1/auth/setup-status")
NEEDS=$(printf '%s' "$STATUS_JSON" | jq -r '.data.needs_setup // .needs_setup // false')

if [ "$NEEDS" != "true" ]; then
  echo "init: admin already exists — skipping bootstrap"
  echo "init: dashboard: http://localhost:8080 (use the credentials you set)"
  exit 0
fi

echo "init: creating admin user '$USERNAME'"
# Bastion's first-time-setup endpoint is /api/v1/auth/bootstrap on v0.2.x.
# The SPA static handler returns HTML 200 OK for any unmatched POST, so we
# can't trust status alone — we check Content-Type and the JSON body shape.
BODY_FILE=$(mktemp)
trap 'rm -f "$BODY_FILE"' EXIT

CODE=$(curl -sS -o "$BODY_FILE" -w '%{http_code}' -X POST \
  "$API/api/v1/auth/bootstrap" \
  -H "Content-Type: application/json" \
  -d "{\"username\":\"$USERNAME\",\"password\":\"$PASSWORD\"}")

if [ "$CODE" != "201" ]; then
  echo "init: ERROR bootstrap failed — HTTP $CODE" >&2
  cat "$BODY_FILE" >&2
  exit 1
fi

USERNAME_OUT=$(jq -r '.data.username // empty' "$BODY_FILE" 2>/dev/null || true)
if [ "$USERNAME_OUT" != "$USERNAME" ]; then
  echo "init: ERROR bootstrap response did not match expected shape" >&2
  cat "$BODY_FILE" >&2
  exit 1
fi

echo "init: admin created — $USERNAME_OUT"
echo "init: dashboard: http://localhost:8080"
echo "init: login as $USERNAME / $PASSWORD"
