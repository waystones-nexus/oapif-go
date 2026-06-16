package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/waystones/oapif-go/internal/api"
	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/db"
)

// startTime is set at package init so it captures process start as early as possible.
var startTime = time.Now()

func main() {
	logStartup("process start")

	cfg := config.Load()
	logStartup("collection config loaded (%d collection(s))", len(cfg.Collections))

	ctx := context.Background()

	// Init S3 client (fast — no DuckDB required).
	s3c := db.NewS3Client(cfg)

	// Load sidecars from R2 via S3 SDK — no DuckDB needed.
	// Collections without a sidecar are detected later, after DuckDB is ready.
	needsDetection := make([]bool, len(cfg.Collections))
	for i := range cfg.Collections {
		c := &cfg.Collections[i]
		sidecar, err := db.TryLoadSidecar(ctx, s3c, cfg.S3Bucket, c.ParquetKey)
		if err != nil {
			log.Printf("[init] warn: sidecar for %s: %v", c.ID, err)
		}
		if sidecar != nil && sidecar.Version == 1 {
			db.ApplySidecar(c, sidecar)
		} else {
			needsDetection[i] = true
		}
	}
	logStartup("sidecars loaded from R2")

	// Create dbReady channel and handler before binding the port.
	dbReady := make(chan struct{})
	h := api.NewHandler(cfg, dbReady, startTime)

	// OGC API routes go through the full middleware chain (gzip, f-param).
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /{$}", h.LandingPage)
	apiMux.HandleFunc("GET /conformance", h.Conformance)
	apiMux.HandleFunc("GET /queryables", h.GlobalQueryables)
	apiMux.HandleFunc("GET /collections", h.Collections)
	apiMux.HandleFunc("GET /collections/{collectionId}", h.Collection)
	apiMux.HandleFunc("GET /collections/{collectionId}/queryables", h.Queryables)
	apiMux.HandleFunc("GET /collections/{collectionId}/items", h.Items)
	apiMux.HandleFunc("GET /collections/{collectionId}/items/{featureId}", h.Item)
	apiMux.HandleFunc("GET /api", h.OpenAPI)
	apiMux.HandleFunc("GET /api.html", h.OpenAPIHTML)

	// outerMux: /health bypasses gzip (tiny response); everything else goes
	// through gzip → f-param → lazy-init → apiMux.
	outerMux := http.NewServeMux()
	outerMux.HandleFunc("GET /health", h.Health)
	fetcher := func(ctx context.Context, bucket, key string) (*config.MetaSidecar, error) {
		return db.TryLoadSidecar(ctx, s3c, bucket, key)
	}
	outerMux.Handle("/",
		api.GzipMiddleware(
			api.FParamMiddleware(
				api.LazyInitMiddleware(cfg, []byte(os.Getenv("LAZY_INIT_SECRET")), fetcher, apiMux),
			),
		),
	)

	// Outermost layer: CORS (all requests) → HEAD support → outerMux.
	handler := api.CORSMiddleware(cfg.CORSAllowedOrigins)(
		api.HeadMiddleware(outerMux),
	)

	// Bind port before DuckDB starts so health checks succeed immediately.
	addr := ":" + cfg.Port
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("listen %s: %v", addr, err)
	}
	logStartup("HTTP server listening on %s", addr)

	// Serve HTTP in goroutine; DuckDB init runs below in main goroutine.
	go func() {
		if err := http.Serve(ln, handler); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	// Init DuckDB + extensions + S3 credentials in main goroutine.
	store, err := db.Open(ctx)
	if err != nil {
		log.Fatalf("[startup] open duckdb: %v", err)
	}
	defer store.Close()

	if err := store.ConfigureS3(ctx, cfg); err != nil {
		log.Fatalf("[startup] configure R2: %v", err)
	}

	// Detect schema for collections that had no sidecar; fix geometry type for all.
	// needsDetection only covers collections present at startup; collections
	// added later by lazy-init (i >= len) always need full detection.
	for i := range cfg.Collections {
		c := &cfg.Collections[i]
		tCol := time.Now()
		if i < len(needsDetection) && !needsDetection[i] {
			// Sidecar loaded — only fix up geometry type from GeoParquet metadata,
			// since the sidecar may have stored "polygon" when WKB type detection failed.
			store.DetectGeomType(ctx, c, cfg.S3Bucket)
			log.Printf("[startup] %dms - %s: geom type corrected to %q (%dms)", time.Since(startTime).Milliseconds(), c.ID, c.GeometryType, time.Since(tCol).Milliseconds())
			continue
		}
		if err := store.DetectColumns(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[init] warn: detect columns for %s: %v", c.ID, err)
		}
		if err := store.CacheExtent(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[init] warn: extent for %s: %v", c.ID, err)
		}
		if err := store.CacheQueryables(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[init] warn: queryables for %s: %v", c.ID, err)
		}
		log.Printf("[startup] %dms - %s: ready (%dms)", time.Since(startTime).Milliseconds(), c.ID, time.Since(tCol).Milliseconds())
	}

	if err := store.Warmup(ctx, cfg.Collections, cfg.S3Bucket); err != nil {
		log.Printf("[startup] warn: warmup: %v", err)
	}

	// Signal readiness: h.store is set then dbReady is closed.
	h.SetDB(store)
	logStartup("DuckDB ready (extensions loaded, warmup complete)")

	// Block main so defer store.Close() fires on signal/panic.
	select {}
}

func logStartup(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[startup] %dms - %s", time.Since(startTime).Milliseconds(), msg)
}

