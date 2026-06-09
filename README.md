# ⚡ oapif-go

A high-performance, lightweight OGC API Features (OAPIF) server written in Go, backed by DuckDB + GeoParquet, and optimized for serverless deployments on Cloudflare R2 or standard AWS S3.

**Demo:** [demo.waystones.cloud](https://demo.waystones.cloud)

Designed as a modern, compilation-backed alternative to Python-based runtimes like `pygeoapi`, `oapif-go` achieves **sub-300ms container cold starts** and lightning-fast spatial queries directly on parquet datasets without the overhead of a dedicated spatial database.


---

## ✨ Features

- ⚡ **Ultra-Fast Startup**: Under 300ms container cold start (compared to ~1.3s for Python-based alternatives).
- 🦆 **Object Storage Native**: Queries GeoParquet files directly on S3/R2 via DuckDB's `httpfs` and `spatial` extensions.
- 🗺️ **OGC API Features Core**: Fully implements Collections, Items, Queryables, Conformance, and OpenAPI specifications.
- 🔮 **Advanced Query Engine**:
  - **CQL2-Text** filter parsing & execution.
  - Spatial filtering via **Bounding Box (bbox)** with custom **bbox-crs**.
  - **Temporal filtering** (date-time intervals, open-ended intervals).
  - **Output CRS transformation** (e.g. reprojecting to Web Mercator on the fly).
  - Pagination, limit, offset, and property-specific equality filters.
- 🔒 **Production Ready**: Supports multi-stage Docker builds: a `minimal` target for serverless containers, and a `gateway` target bundled with Caddy for automatic TLS termination.
- 🎨 **Branding & Customization**: White-label configuration support to inject metadata, provider info, licenses, and custom contact details.

---

## 🛠️ Prerequisites

- Docker 24+
- A Cloudflare R2 or AWS S3 bucket containing one or more GeoParquet files

---

## ⚙️ Configuration

The server is configured dynamically via environment variables or a configuration JSON file.

### Core & Storage Settings

| Variable                  | Required | Default                      | Description                                                                                     |
|---------------------------|----------|------------------------------|-------------------------------------------------------------------------------------------------|
| `S3_ACCESS_KEY_ID`        | yes      | —                            | Access key ID (fallbacks: `AWS_ACCESS_KEY_ID`, `R2_ACCESS_KEY_ID`)                             |
| `S3_SECRET_ACCESS_KEY`    | yes      | —                            | Secret access key (fallbacks: `AWS_SECRET_ACCESS_KEY`, `R2_SECRET_ACCESS_KEY`)                 |
| `S3_BUCKET`               | yes      | —                            | Bucket name (fallback: `R2_BUCKET`)                                                             |
| `S3_ENDPOINT`             | no       | —                            | Custom endpoint URL for R2/MinIO/etc. Omit for AWS S3. (fallbacks: `AWS_ENDPOINT_URL`, `R2_ENDPOINT`) |
| `S3_REGION`               | no       | `auto` / `us-east-1`         | Region. Defaults to `auto` when endpoint is set, `us-east-1` for standard S3.                   |
| `S3_URL_STYLE`            | no       | `path` / `vhost`             | URL style. Defaults to `path` when endpoint is set, `vhost` for standard S3.                    |
| `SERVER_URL`              | yes      | `http://localhost:5000`      | Public base URL used in OGC API self-links (fallback: `PYGEOAPI_SERVER_URL`)                   |
| `PORT` / `CONTAINER_PORT` | no       | `5000`                       | HTTP listen port                                                                                |
| `CONFIG_PATH`             | no       | `./config.json`              | Path to multi-collection JSON config                                                            |
| `COLLECTION_CONFIG_B64`   | no       | —                            | Base64-encoded `config.json` content (takes effect if no file is present at `CONFIG_PATH`)       |
| `MODEL_PATH`              | no       | —                            | Alternative path to a Waystones `model.json` to load collections from                           |
| `MODEL_LAYER_KEY_PREFIX`  | no       | —                            | S3 key prefix prepended to parquet keys when loading from `model.json`                          |

### Metadata & Branding Settings

Inject metadata for white-label branding on the landing page and OpenAPI document:

| Variable               | Required | Default                       | Description                               |
|------------------------|----------|-------------------------------|-------------------------------------------|
| `SERVER_TITLE`         | no       | `Waystones OGC API Features`  | Title of the OGC API landing page         |
| `SERVER_DESCRIPTION`   | no       | —                             | Description of the service                |
| `SERVER_PROVIDER`      | no       | —                             | Name of the service provider              |
| `SERVER_LICENSE`       | no       | —                             | License under which the data is provided  |
| `SERVER_KEYWORDS`      | no       | —                             | Comma-separated list of keywords          |
| `SERVER_CONTACT_EMAIL` | no       | —                             | Contact email for the service provider    |
| `SERVER_CONTACT_NAME`  | no       | —                             | Contact name for the service provider     |

---

### Collection Setup

#### Single Collection (via Env Vars)

For quick setups, configure a single dataset via environment variables:

```sh
COLLECTION_ID=my-dataset
COLLECTION_TITLE="My Dataset"
COLLECTION_PARQUET_KEY=projects/abc/data.parquet # fallback: COLLECTION_R2_KEY
COLLECTION_GEOM_COLUMN=geometry                  # default
COLLECTION_ID_COLUMN=fid                         # default
```

#### Multi-Collection (via `config.json`)

For serving multiple layers with custom options:

```json
{
  "title": "Custom Server Title",
  "description": "Custom Server Description",
  "provider": "Provider Name",
  "license": "CC-BY-4.0",
  "keywords": ["geospatial", "duckdb"],
  "contact_email": "info@example.com",
  "contact_name": "Data Team",
  "collections": [
    {
      "id": "my-dataset",
      "title": "My Dataset",
      "description": "Optional description",
      "parquet_key": "projects/abc/data.parquet",
      "geom_column": "geometry",
      "id_column": "fid",
      "crs": "CRS84",
      "supported_crs": [
        "http://www.opengis.net/def/crs/EPSG/0/3857"
      ]
    }
  ]
}
```

---

## 🚀 Build & Run

### Local Development (Docker Compose)

Create a `.env` file in the root directory:

```sh
# Storage configuration (Cloudflare R2 example)
S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com
S3_ACCESS_KEY_ID=<key>
S3_SECRET_ACCESS_KEY=<secret>
S3_BUCKET=my-bucket

# Define collection
COLLECTION_ID=my-dataset
COLLECTION_PARQUET_KEY=path/to/data.parquet
```

Then start the application:

```sh
docker compose up --build
```

### Build Targets

The `Dockerfile` supports two multi-stage build targets:

| Target    | Command                                    | Description                                                     |
|-----------|--------------------------------------------|-----------------------------------------------------------------|
| `minimal` | `docker build --target minimal .`          | Production-ready minimal Go app container (default deployment)   |
| `gateway` | `docker build --target gateway .`          | Self-hosted bundle containing both the Go server and Caddy TLS  |

---

## 🔍 OGC API Features & Query Support

The server supports the OGC API Features core endpoints and query parameters.

### Query Parameters

| Parameter      | Type     | Description                                                                                                   |
|----------------|----------|---------------------------------------------------------------------------------------------------------------|
| `limit`        | Integer  | Number of features to return (min: `1`, max: `1000`, default: `10`)                                           |
| `offset`       | Integer  | Index offset of the first feature to return (default: `0`)                                                   |
| `bbox`         | String   | `minx,miny,maxx,maxy` spatial filter                                                                          |
| `bbox-crs`     | String   | CRS URI for the `bbox` parameter (e.g. `http://www.opengis.net/def/crs/OGC/1.3/CRS84`)                        |
| `datetime`     | String   | RFC3339 timestamp or interval (e.g., `2026-06-03T12:00:00Z` or `2026-06-01T00:00:00Z/2026-06-03T00:00:00Z` or `../2026-06-03T00:00:00Z`) |
| `crs`          | String   | Requested output coordinate reference system URI                                                              |
| `filter`       | String   | CQL2-Text filter expression                                                                                   |
| `filter-lang`  | String   | Language of the filter parameter. Only `cql2-text` is supported (default: `cql2-text`)                        |
| `f`            | String   | Format response format. Set `f=json` to force JSON output and bypass browser preference                       |

Property equality filters are also supported as direct query parameters (e.g. `?status=active` or `?name=foo`).

### CQL2-Text Filter Support

The server parses and translates standard **CQL2-Text** filters into parameterized DuckDB SQL expressions. Supported expressions include:

- **Logical Connectors**: `AND`, `OR`, `NOT`
- **Comparison Operators**: `=`, `<>`, `<`, `>`, `<=`, `>=`
- **String Pattern Matching**: `LIKE` / `NOT LIKE` (e.g., `properties.name LIKE 'Waystone%'`)
- **Set Inclusions**: `IN` / `NOT IN` (e.g., `properties.status IN ('active', 'pending')`)
- **Range Queries**: `BETWEEN` / `NOT BETWEEN` (e.g., `properties.elevation BETWEEN 100 AND 500`)
- **Null Checks**: `IS NULL` / `IS NOT NULL` (e.g., `properties.description IS NOT NULL`)
- **Precedence Grouping**: Parentheses `()` to enforce complex logical evaluation

---

## ⚡ Performance & Benchmarking

Run the minimal container locally to inspect cold start performance:

```sh
docker run --rm \
  -e S3_ENDPOINT=https://<account-id>.r2.cloudflarestorage.com \
  -e S3_ACCESS_KEY_ID=<key> \
  -e S3_SECRET_ACCESS_KEY=<secret> \
  -e S3_BUCKET=my-bucket \
  -e COLLECTION_ID=my-dataset \
  -e COLLECTION_PARQUET_KEY=path/to/data.parquet \
  -p 5000:5000 \
  waystones-oapif
```

Expected startup & warmup latency logs:

```
[startup] 0ms   - process start
[startup] 2ms   - collection config loaded (1 collection(s))
[startup] 5ms   - DuckDB opened, extensions loaded
[startup] 13ms  - R2 credentials configured
[startup] 180ms - extents and queryables cached
[startup] 210ms - warmup queries complete
[startup] 211ms - HTTP server listening on :5000
[ttfb]    310ms - first /items request received after startup
```

The service target is a total startup time (`HTTP server listening`) under **300ms** and first `/items` TTFB under **400ms**.

---

## 📡 API & Testing Endpoints

```sh
# Landing page (HTML or JSON)
curl http://localhost:5000/

# Conformance declaration
curl http://localhost:5000/conformance

# OpenAPI Definition (JSON & Interactive Swagger UI)
curl http://localhost:5000/api
curl http://localhost:5000/api.html

# List collections
curl http://localhost:5000/collections

# Single collection metadata
curl http://localhost:5000/collections/my-dataset

# Queryable fields schema
curl http://localhost:5000/collections/my-dataset/queryables

# Fetch first 10 items
curl "http://localhost:5000/collections/my-dataset/items?limit=10"

# Filter items by Bounding Box (bbox)
curl "http://localhost:5000/collections/my-dataset/items?bbox=-10,-10,10,10&limit=10"

# Reproject items to a different coordinate system (Output CRS)
curl "http://localhost:5000/collections/my-dataset/items?crs=http://www.opengis.net/def/crs/EPSG/0/3857&limit=5"

# CQL2-Text Filtering
curl "http://localhost:5000/collections/my-dataset/items?filter=properties.status%20%3D%20%27active%27%20AND%20properties.elevation%20%3E%20100"

# Fetch single item by ID
curl http://localhost:5000/collections/my-dataset/items/1
```

---

## 🔒 DuckDB Version Pinned Constraints

DuckDB extensions (`spatial`, `httpfs`) pre-baked during the Docker build must **exactly match** the DuckDB version bundled within the Go binary.

The project enforces these versions for stability and correctness:
- **Go Library**: `github.com/duckdb/duckdb-go/v2@v2.10503.1`
- **DuckDB Core Engine & Extensions**: `v1.5.3`

If you update the library version in `go.mod` (or via `go get`), inspect the compiled DuckDB header:

```sh
# Inspect the downloaded module source header
find $(go env GOPATH)/pkg/mod/github.com/duckdb/duckdb-go -name 'duckdb.h' | \
  xargs grep -m1 DUCKDB_VERSION
```

And update `DUCKDB_VERSION` in the `Dockerfile` to match:

```dockerfile
ARG DUCKDB_VERSION=v1.5.3
```

---

## 🏗️ Architecture

```
Cloudflare Container (minimal target)
  └─ /server  (Go binary)
       ├─ DuckDB in-memory (1 connection pool instance)
       │    ├─ spatial extension  (pre-baked at /extensions/)
       │    └─ httpfs extension   (pre-baked at /extensions/)
       └─ R2 access via DuckDB S3 secret (no AWS SDK)
```

For self-hosted deployments using the `gateway` target, Caddy sits in front and handles TLS termination.
