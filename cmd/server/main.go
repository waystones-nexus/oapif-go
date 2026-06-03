package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
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

	store, err := db.Open(ctx)
	if err != nil {
		log.Fatalf("[startup] open duckdb: %v", err)
	}
	defer store.Close()
	logStartup("DuckDB opened, extensions loaded")

	if err := store.ConfigureS3(ctx, cfg); err != nil {
		log.Fatalf("[startup] configure R2: %v", err)
	}
	logStartup("R2 credentials configured")

	for i := range cfg.Collections {
		c := &cfg.Collections[i]
		tCol := time.Now()
		initCollection(ctx, cfg, store, c)
		log.Printf("[startup] %dms - %s: ready (%dms)", time.Since(startTime).Milliseconds(), c.ID, time.Since(tCol).Milliseconds())
	}
	logStartup("extents and queryables cached")

	if err := store.Warmup(ctx, cfg.Collections, cfg.S3Bucket); err != nil {
		log.Printf("[startup] warn: warmup: %v", err)
	}
	logStartup("warmup queries complete")

	h := api.NewHandler(cfg, store, startTime)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.LandingPage)
	mux.HandleFunc("GET /conformance", h.Conformance)
	mux.HandleFunc("GET /collections", h.Collections)
	mux.HandleFunc("GET /collections/{collectionId}", h.Collection)
	mux.HandleFunc("GET /collections/{collectionId}/queryables", h.Queryables)
	mux.HandleFunc("GET /collections/{collectionId}/items", h.Items)
	mux.HandleFunc("GET /collections/{collectionId}/items/{featureId}", h.Item)
	mux.HandleFunc("GET /api", h.OpenAPI)
	mux.HandleFunc("GET /api.html", h.OpenAPIHTML)

	addr := ":" + cfg.Port
	logStartup("HTTP server listening on %s", addr)

	if err := http.ListenAndServe(addr, lazyInitMiddleware(cfg, store, mux)); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func logStartup(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[startup] %dms - %s", time.Since(startTime).Milliseconds(), msg)
}

// initCollection runs the per-collection startup sequence: try sidecar first,
// fall back to full parquet scan if the sidecar is absent or stale.
func initCollection(ctx context.Context, cfg *config.Config, store *db.Store, c *config.CollectionConfig) {
	sidecar, err := store.TryLoadSidecar(ctx, c.ParquetKey, cfg.S3Bucket)
	if err != nil {
		log.Printf("[init] warn: sidecar for %s: %v", c.ID, err)
	}
	if sidecar != nil && sidecar.Version == 1 {
		db.ApplySidecar(c, sidecar)
		return
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
}

// lazyInitMiddleware reads X-Waystones-OapifGo-B64 on the first request when
// the server started with 0 collections. This is the fallback path when
// COLLECTION_CONFIG_B64 was silently dropped by Cloudflare Containers due to
// the 5 KB total env var limit on large configs with many collections.
func lazyInitMiddleware(cfg *config.Config, store *db.Store, next http.Handler) http.Handler {
	var (
		mu   sync.Mutex
		done bool
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !done && len(cfg.Collections) == 0 {
			if hdr := r.Header.Get("X-Waystones-OapifGo-B64"); hdr != "" {
				mu.Lock()
				if !done {
					if err := config.ApplyB64(cfg, hdr); err != nil {
						log.Printf("[lazy-init] failed to apply header config: %v", err)
					} else {
						ctx := r.Context()
						for i := range cfg.Collections {
							initCollection(ctx, cfg, store, &cfg.Collections[i])
						}
						done = len(cfg.Collections) > 0
						log.Printf("[lazy-init] configured %d collection(s) from X-Waystones-OapifGo-B64", len(cfg.Collections))
					}
				}
				mu.Unlock()
			}
		}
		next.ServeHTTP(w, r)
	})
}
