# ── Stage 0: Caddy binary (for the gateway target only) ───────────────────
FROM caddy:latest AS caddy-bin

# ── Stage 1: Build Go binary ───────────────────────────────────────────────
FROM golang:1.22-bookworm AS builder

WORKDIR /app

# Fetch dependencies first for layer-cache efficiency.
# We use go mod tidy + go get to avoid requiring a pre-committed go.sum.
COPY go.mod ./
RUN go get github.com/duckdb/duckdb-go/v2@latest && go mod tidy

COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -o /app/server ./cmd/server

# ── Stage 2: Pre-bake DuckDB extensions ───────────────────────────────────
FROM golang:1.22-bookworm AS extensions

# DUCKDB_VERSION must match the version embedded in github.com/duckdb/duckdb-go/v2.
#
# Find it after your first build:
#   docker build --target builder -t oapif-builder .
#   docker run --rm oapif-builder /app/server --version  # not implemented, use:
#   grep -r 'DUCKDB_VERSION\|duckdb_version' \
#     $(go env GOPATH)/pkg/mod/github.com/duckdb/duckdb-go/v2*/
#
# Or check the go-duckdb release notes for the bundled DuckDB version.
ARG DUCKDB_VERSION=v1.5.2

RUN apt-get update && apt-get install -y --no-install-recommends curl && \
    rm -rf /var/lib/apt/lists/*

RUN mkdir -p /extensions && \
    curl -fsSL \
      "https://extensions.duckdb.org/${DUCKDB_VERSION}/linux_amd64/spatial.duckdb_extension.gz" \
      | gunzip > /extensions/spatial.duckdb_extension && \
    curl -fsSL \
      "https://extensions.duckdb.org/${DUCKDB_VERSION}/linux_amd64/httpfs.duckdb_extension.gz" \
      | gunzip > /extensions/httpfs.duckdb_extension

# ── Stage 3a: minimal — Cloudflare Containers (direct HTTP, no Caddy) ─────
FROM debian:bookworm-slim AS minimal

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && \
    rm -rf /var/lib/apt/lists/*

COPY --from=builder /app/server /server
COPY --from=extensions /extensions /extensions

ENV DUCKDB_EXTENSION_DIR=/extensions

EXPOSE 5000
CMD ["/server"]

# ── Stage 3b: gateway — OSS / self-hosted (Caddy TLS sidecar) ─────────────
FROM minimal AS gateway

COPY --from=caddy-bin /usr/bin/caddy /usr/bin/caddy
COPY boot.sh /boot.sh
COPY Caddyfile /Caddyfile
COPY Caddyfile.tls /Caddyfile.tls
RUN chmod +x /boot.sh

EXPOSE 5000 443
CMD ["/boot.sh"]
