package api

import (
	"context"
	"log"
	"net/http"
	"sync"

	"github.com/waystones/oapif-go/internal/auth"
	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/db"
)

// SidecarFetcher loads a collection sidecar given a bucket and parquet key.
// Returns (nil, nil) when the sidecar is absent; (nil, err) on parse error.
type SidecarFetcher func(ctx context.Context, bucket, parquetKey string) (*config.MetaSidecar, error)

// LazyInitMiddleware reads X-Waystones-OapifGo-B64 on the first request when
// the server started with 0 collections. Sidecars are loaded via fetchSidecar.
// Collections without a sidecar work with config defaults; extent/queryables
// won't be cached in this path.
//
// Security: when secret is non-empty, the header is only accepted when
// accompanied by a matching X-Waystones-OapifGo-Sig header containing the
// HMAC-SHA256 hex digest of the B64 payload.
// Pass nil/empty secret to skip signature verification (self-hosted / local dev).
func LazyInitMiddleware(cfg *config.Config, secret []byte, fetchSidecar SidecarFetcher, next http.Handler) http.Handler {
	var (
		mu   sync.Mutex
		done bool
	)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !done && len(cfg.Collections) == 0 {
			if hdr := r.Header.Get("X-Waystones-OapifGo-B64"); hdr != "" {
				if len(secret) > 0 {
					sig := r.Header.Get("X-Waystones-OapifGo-Sig")
					if !auth.VerifyHMAC(secret, hdr, sig) {
						log.Printf("[lazy-init] rejected: invalid or missing HMAC signature")
						next.ServeHTTP(w, r)
						return
					}
				}
				mu.Lock()
				if !done {
					if err := config.ApplyB64(cfg, hdr); err != nil {
						log.Printf("[lazy-init] failed to apply header config: %v", err)
					} else {
						ctx := r.Context()
						for i := range cfg.Collections {
							c := &cfg.Collections[i]
							sidecar, err := fetchSidecar(ctx, cfg.S3Bucket, c.ParquetKey)
							if err != nil {
								log.Printf("[lazy-init] warn: sidecar for %s: %v", c.ID, err)
							}
							if sidecar != nil && sidecar.Version == 1 {
								db.ApplySidecar(c, sidecar)
							}
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
