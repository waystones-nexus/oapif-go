# oapif-go

A Go-based OGC API Features server backed by DuckDB + GeoParquet on Cloudflare R2.

Spike goal: validate that a Go binary with go-duckdb can replace pygeoapi as the OGC API
Features runtime and hit **<300ms container cold start** (vs ~1.3s for Python + pygeoapi).

---

## Prerequisites

- Docker 24+
- A Cloudflare R2 bucket containing at least one GeoParquet file

---

## Configuration

All configuration is via environment variables.

| Variable                    | Required | Default                     | Description                                                   |
|-----------------------------|----------|-----------------------------|---------------------------------------------------------------|
| `S3_ACCESS_KEY_ID`          | yes      | —                           | Access key (`R2_ACCESS_KEY_ID` accepted as fallback)          |
| `S3_SECRET_ACCESS_KEY`      | yes      | —                           | Secret key (`R2_SECRET_ACCESS_KEY` accepted as fallback)      |
| `S3_BUCKET`                 | yes      | —                           | Bucket name (`R2_BUCKET` accepted as fallback)                |
| `S3_ENDPOINT`               | no       | —                           | Custom endpoint URL for R2/MinIO/etc. Omit for AWS S3. (`R2_ENDPOINT` accepted as fallback) |
| `S3_REGION`                 | no       | `auto` / `us-east-1`        | Region. Defaults to `auto` when endpoint is set, `us-east-1` for AWS S3 |
| `S3_URL_STYLE`              | no       | `path` / `vhost`            | URL style. Defaults to `path` when endpoint is set, `vhost` for AWS S3  |
| `SERVER_URL`                | yes      | `http://localhost:5000`     | Public base URL used in OGC API self-links                    |
| `PORT` / `CONTAINER_PORT`   | no       | `5000`                      | HTTP listen port                                              |
| `SERVER_TITLE`              | no       | `Waystones OGC API Features`| Landing page title                                            |
| `CONFIG_PATH`               | no       | `./config.json`             | Path to multi-collection JSON config                          |

### Single collection (env vars)

```sh
COLLECTION_ID=my-dataset
COLLECTION_TITLE="My Dataset"
COLLECTION_PARQUET_KEY=projects/abc/data.parquet
COLLECTION_GEOM_COLUMN=geometry   # default
COLLECTION_ID_COLUMN=fid          # default
```

### Multi-collection (config.json)

```json
{
  "collections": [
    {
      "id": "my-dataset",
      "title": "My Dataset",
      "description": "Optional description",
      "parquet_key": "projects/abc/data.parquet",
      "geom_column": "geometry",
      "id_column": "fid",
      "crs": "CRS84"
    }
  ]
}
```

---

## Build & Run

### Local (docker-compose)

Create a `.env` file:

```sh
# Cloudflare R2
S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
S3_ACCESS_KEY_ID=<key>
S3_SECRET_ACCESS_KEY=<secret>
S3_BUCKET=my-bucket

# AWS S3 (omit S3_ENDPOINT — region and URL style default automatically)
# S3_ACCESS_KEY_ID=<key>
# S3_SECRET_ACCESS_KEY=<secret>
# S3_BUCKET=my-bucket
# S3_REGION=eu-west-1

COLLECTION_PARQUET_KEY=path/to/data.parquet
```

Then:

```sh
docker-compose up --build
```

### Build targets

| Target    | Command                                    | Use case                        |
|-----------|--------------------------------------------|---------------------------------|
| `minimal` | `docker build --target minimal .`          | Cloudflare Containers (default) |
| `gateway` | `docker build --target gateway .`          | OSS / self-hosted (Caddy TLS)   |

---

## Measuring cold start

Run the container and watch the `[startup]` log lines:

```sh
docker run --rm \
  -e R2_ENDPOINT=... \
  -e R2_ACCESS_KEY_ID=... \
  -e R2_SECRET_ACCESS_KEY=... \
  -e R2_BUCKET=... \
  -e COLLECTION_R2_KEY=... \
  -p 5000:5000 \
  waystones-oapif
```

Expected output:

```
[startup] 0ms   - process start
[startup] 2ms   - collection config loaded (1 collection(s))
[startup] 5ms   - DuckDB connector created
[startup] 12ms  - extensions loaded (first connection established)
[startup] 13ms  - R2 credentials configured
[startup] 180ms - extents and queryables cached
[startup] 210ms - warmup queries complete
[startup] 211ms - HTTP server listening on :5000
[ttfb]    310ms - first /items request received after startup
```

The spike is successful when total startup (`HTTP server listening`) is under **300ms** and
first `/items` TTFB is under **400ms**.

---

## Test endpoints (curl)

```sh
# Landing page
curl http://localhost:5000/

# Conformance
curl http://localhost:5000/conformance

# List collections
curl http://localhost:5000/collections

# Single collection
curl http://localhost:5000/collections/my-dataset

# Queryable fields
curl http://localhost:5000/collections/my-dataset/queryables

# First 10 features
curl "http://localhost:5000/collections/my-dataset/items?limit=10"

# Bbox filter
curl "http://localhost:5000/collections/my-dataset/items?bbox=-10,-10,10,10&limit=10"

# Pagination
curl "http://localhost:5000/collections/my-dataset/items?limit=10&offset=10"

# Single feature
curl http://localhost:5000/collections/my-dataset/items/1
```

---

## DuckDB version constraint

The DuckDB extensions (`spatial`, `httpfs`) downloaded during `docker build` must **exactly
match** the DuckDB version bundled in `github.com/duckdb/duckdb-go/v2`.

### Finding the correct version

After your first successful build, check the embedded DuckDB version:

```sh
# Option 1: inspect the downloaded module source
find $(go env GOPATH)/pkg/mod/github.com/duckdb/duckdb-go -name 'duckdb.h' | \
  xargs grep -m1 DUCKDB_VERSION

# Option 2: run a version query (requires a running container)
docker exec -it <container> sh -c \
  'echo "SELECT version();" | /server'  # not implemented; use psql or curl /items
```

Then update `DUCKDB_VERSION` in the Dockerfile:

```dockerfile
ARG DUCKDB_VERSION=v1.x.x   # set to the exact version reported above
```

Extension URLs follow this pattern:
`https://extensions.duckdb.org/<version>/linux_amd64/<ext>.duckdb_extension.gz`

---

## Architecture

```
Cloudflare Container (minimal target)
  └─ /server  (Go binary)
       ├─ DuckDB in-memory (4 connections)
       │    ├─ spatial extension  (pre-baked at /extensions/)
       │    └─ httpfs extension   (pre-baked at /extensions/)
       └─ R2 access via DuckDB S3 secret (no AWS SDK)
```

For self-hosted / OSS deployments using the gateway target, Caddy sits in front and
handles TLS termination.
