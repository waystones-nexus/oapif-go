# ── Stage 0: Caddy binary (for the gateway target only) ───────────────────
FROM caddy:2.10.0-alpine AS caddy-bin

# ── Stage 1: Build Go binary ───────────────────────────────────────────────
FROM golang:1.24-bookworm AS builder

# go-duckdb bundles DuckDB's C++ source and compiles it via CGO — needs g++.
RUN apt-get update && apt-get install -y --no-install-recommends \
    build-essential \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY . .

# Pin the go-duckdb version so builds are reproducible.
# The module version encodes the bundled DuckDB version:
#   v2.XYYZZP → DuckDB vX.YY.ZZ  (e.g. v2.10503.1 → DuckDB v1.5.3)
# Update both this and DUCKDB_VERSION below together.
#
# BuildKit cache mounts keep the Go module and build caches between runs
# so repeated builds don't re-download or re-compile DuckDB from scratch.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go get github.com/duckdb/duckdb-go/v2@v2.10503.1 && \
    go mod tidy && \
    CGO_ENABLED=1 GOOS=linux go build -v -o /app/server ./cmd/server

# ── Stage 2: Pre-bake DuckDB extensions ───────────────────────────────────
FROM golang:1.24-bookworm AS extensions

# Must match the DuckDB version bundled in go-duckdb (see builder stage comment).
ARG DUCKDB_VERSION=v1.5.3

RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /extensions/${DUCKDB_VERSION}/linux_amd64 && \
    curl -fsSL \
      "https://extensions.duckdb.org/${DUCKDB_VERSION}/linux_amd64/spatial.duckdb_extension.gz" \
      | gunzip > /extensions/${DUCKDB_VERSION}/linux_amd64/spatial.duckdb_extension && \
    curl -fsSL \
      "https://extensions.duckdb.org/${DUCKDB_VERSION}/linux_amd64/httpfs.duckdb_extension.gz" \
      | gunzip > /extensions/${DUCKDB_VERSION}/linux_amd64/httpfs.duckdb_extension

# ── Stage 3a: minimal — Cloudflare Containers (direct HTTP, no Caddy) ─────
FROM debian:bookworm-slim AS minimal

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/server /server
COPY --from=extensions /extensions /extensions

ENV DUCKDB_EXTENSION_DIR=/extensions

# Run as a non-root user — reduces container breakout blast radius.
RUN adduser --disabled-password --gecos '' --uid 1001 app
USER app

EXPOSE 5000
CMD ["/server"]

# ── Stage 3b: gateway — OSS / self-hosted (Caddy TLS sidecar) ─────────────
FROM minimal AS gateway

# Switch back to root: Caddy binds port 443 which requires root or
# CAP_NET_BIND_SERVICE on most Linux kernels. The non-root security gain
# is in the minimal (Cloudflare Containers) target where no privileged
# port is needed and the Go binary runs as UID 1001.
USER root
COPY --from=caddy-bin /usr/bin/caddy /usr/bin/caddy
COPY boot.sh /boot.sh
COPY Caddyfile /Caddyfile
COPY Caddyfile.tls /Caddyfile.tls
RUN chmod +x /boot.sh

EXPOSE 5000 443
CMD ["/boot.sh"]
