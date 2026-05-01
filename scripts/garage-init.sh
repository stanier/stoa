#!/bin/sh
# Idempotent Garage single-node bootstrap via the garage CLI (RPC).
# Runs in a garage-init image (alpine + garage binary).
#
# Required env vars (set in docker-compose / .env):
#   GARAGE_RPC_SECRET        — RPC network secret
#   GARAGE_BUCKET            — bucket name (default: uploads)
#   GARAGE_ACCESS_KEY_ID     — S3 access key ID
#   GARAGE_SECRET_ACCESS_KEY — S3 secret access key
#
# GARAGE_ADMIN_TOKEN is NOT required: node discovery uses the RPC protocol
# directly — we wait until `garage status` succeeds.

set -e

BUCKET="${GARAGE_BUCKET:-uploads}"
GARAGE_HOST="${GARAGE_HOST:-garage}"
GARAGE_RPC_PORT="${GARAGE_RPC_PORT:-3901}"

# ---------------------------------------------------------------------------
# Wait for garage's RPC port to be open (TCP-level)
# ---------------------------------------------------------------------------
echo "garage-init: waiting for garage RPC at $GARAGE_HOST:$GARAGE_RPC_PORT ..."
until nc -z "$GARAGE_HOST" "$GARAGE_RPC_PORT" 2>/dev/null; do
  sleep 2
done
echo "garage-init: RPC port is open"

# Discover the node ID from the garage admin HTTP API.
ADMIN_URL="http://${GARAGE_HOST}:3903"
NODE_ID=""
echo "garage-init: discovering node ID ..."
for _ in $(seq 1 30); do
  NODE_ID=$(curl -sf \
    -H "Authorization: Bearer $GARAGE_ADMIN_TOKEN" \
    "$ADMIN_URL/v2/GetClusterStatus" 2>/dev/null | \
    grep -o '"id": *"[^"]*"' | head -1 | sed 's/.*"\([^"]*\)"/\1/') || true
  if [ -n "$NODE_ID" ]; then
    break
  fi
  sleep 2
done

if [ -z "$NODE_ID" ]; then
  echo "garage-init: ERROR: could not discover node ID from admin API" >&2
  exit 1
fi

NODE_SHORT="${NODE_ID:0:16}"
RPC_HOST="${NODE_ID}@${GARAGE_HOST}:${GARAGE_RPC_PORT}"
echo "garage-init: node $NODE_SHORT @ $GARAGE_HOST:$GARAGE_RPC_PORT"

gcli() {
  garage -h "$RPC_HOST" -s "$GARAGE_RPC_SECRET" "$@"
}

# Wait until the CLI can actually reach the node
echo "garage-init: waiting for garage CLI to connect ..."
until gcli status > /dev/null 2>&1; do
  sleep 2
done
echo "garage-init: connected"

# ---------------------------------------------------------------------------
# Apply layout (idempotent: only assign if layout version is still 0)
# ---------------------------------------------------------------------------
LAYOUT_VER=$(gcli layout show 2>&1 | grep -E "^Current cluster layout version" | awk '{print $NF}' || echo "")
if [ -z "$LAYOUT_VER" ] || [ "$LAYOUT_VER" = "0" ]; then
  echo "garage-init: assigning layout (zone dc1, capacity 1G)"
  gcli layout assign -z dc1 -c 1G "$NODE_SHORT"
  echo "garage-init: applying layout version 1"
  gcli layout apply --version 1
  echo "garage-init: layout applied"
else
  echo "garage-init: layout already at version $LAYOUT_VER, skipping"
fi

# ---------------------------------------------------------------------------
# Create bucket (idempotent)
# ---------------------------------------------------------------------------
if gcli bucket info "$BUCKET" > /dev/null 2>&1; then
  echo "garage-init: bucket '$BUCKET' already exists"
else
  gcli bucket create "$BUCKET"
  echo "garage-init: bucket '$BUCKET' created"
fi

# ---------------------------------------------------------------------------
# Import access key and grant bucket access (idempotent)
# ---------------------------------------------------------------------------
if gcli key info "$GARAGE_ACCESS_KEY_ID" > /dev/null 2>&1; then
  echo "garage-init: access key already exists"
else
  gcli key import -n webd-uploads --yes "$GARAGE_ACCESS_KEY_ID" "$GARAGE_SECRET_ACCESS_KEY"
  gcli bucket allow --read --write --key "$GARAGE_ACCESS_KEY_ID" "$BUCKET"
  echo "garage-init: access key imported and granted access to '$BUCKET'"
fi

echo "garage-init: done"
