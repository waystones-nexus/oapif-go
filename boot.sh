#!/bin/sh
# Gateway target entrypoint — starts Caddy as a TLS sidecar then exec's the Go server.
# Used for OSS/self-hosted deployments. Cloudflare Containers use the minimal target
# which runs /server directly (no Caddy needed).
set -e

export CONTAINER_PORT=${CONTAINER_PORT:-5001}

if [ -n "${TLS_CERT_FILE:-}" ] && [ -n "${TLS_KEY_FILE:-}" ]; then
    echo "[boot] Starting Caddy with TLS on :443 → localhost:${CONTAINER_PORT}"
    caddy run --config /Caddyfile.tls --adapter caddyfile &
else
    echo "[boot] Starting Caddy on :5000 → localhost:${CONTAINER_PORT}"
    caddy run --config /Caddyfile --adapter caddyfile &
fi

exec /server
