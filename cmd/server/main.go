package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
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
		if err := store.DetectColumns(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[startup] warn: detect columns for %s: %v", c.ID, err)
		}
		if err := store.CacheExtent(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[startup] warn: extent for %s: %v", c.ID, err)
		}
		if err := store.CacheQueryables(ctx, c, cfg.S3Bucket); err != nil {
			log.Printf("[startup] warn: queryables for %s: %v", c.ID, err)
		}
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

	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server: %v", err)
	}
}

func logStartup(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[startup] %dms - %s", time.Since(startTime).Milliseconds(), msg)
}
