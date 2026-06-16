package api_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/waystones/oapif-go/internal/api"
	"github.com/waystones/oapif-go/internal/config"
)

// validB64 is a base64-encoded config with one collection.
// echo -n '{"collections":[{"id":"test","parquet_key":"test.parquet"}]}' | base64
const validB64 = "eyJjb2xsZWN0aW9ucyI6W3siaWQiOiJ0ZXN0IiwicGFycXVldF9rZXkiOiJ0ZXN0LnBhcnF1ZXQifV19"

func makeHMACFor(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

func noopFetcher(_ context.Context, _, _ string) (*config.MetaSidecar, error) {
	return nil, nil
}

func newLazyHandler(cfg *config.Config, secret []byte) http.Handler {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return api.LazyInitMiddleware(cfg, secret, noopFetcher, inner)
}

func TestLazyInit_NoSecret_AppliesConfig(t *testing.T) {
	cfg := &config.Config{}
	h := newLazyHandler(cfg, nil)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Waystones-OapifGo-B64", validB64)
	httptest.NewRecorder() // discard response
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(cfg.Collections) != 1 {
		t.Fatalf("expected 1 collection after lazy-init, got %d", len(cfg.Collections))
	}
	if cfg.Collections[0].ID != "test" {
		t.Errorf("expected collection ID 'test', got %q", cfg.Collections[0].ID)
	}
}

func TestLazyInit_ValidHMAC_AppliesConfig(t *testing.T) {
	cfg := &config.Config{}
	secret := []byte("mysecret")
	h := newLazyHandler(cfg, secret)

	sig := makeHMACFor("mysecret", validB64)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Waystones-OapifGo-B64", validB64)
	req.Header.Set("X-Waystones-OapifGo-Sig", sig)
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(cfg.Collections) != 1 {
		t.Fatalf("expected 1 collection after valid HMAC lazy-init, got %d", len(cfg.Collections))
	}
}

func TestLazyInit_InvalidHMAC_Rejected(t *testing.T) {
	cfg := &config.Config{}
	h := newLazyHandler(cfg, []byte("mysecret"))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Waystones-OapifGo-B64", validB64)
	req.Header.Set("X-Waystones-OapifGo-Sig", "deadbeef")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// Request should pass through (200) but config must not be applied.
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if len(cfg.Collections) != 0 {
		t.Errorf("expected 0 collections after rejected HMAC, got %d", len(cfg.Collections))
	}
}

func TestLazyInit_MissingSig_Rejected(t *testing.T) {
	cfg := &config.Config{}
	h := newLazyHandler(cfg, []byte("mysecret"))

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("X-Waystones-OapifGo-B64", validB64)
	// No Sig header
	h.ServeHTTP(httptest.NewRecorder(), req)

	if len(cfg.Collections) != 0 {
		t.Errorf("expected 0 collections when sig is missing, got %d", len(cfg.Collections))
	}
}

func TestLazyInit_DoubleInit_IsNoOp(t *testing.T) {
	cfg := &config.Config{}
	h := newLazyHandler(cfg, nil)

	send := func() {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("X-Waystones-OapifGo-B64", validB64)
		h.ServeHTTP(httptest.NewRecorder(), req)
	}

	send()
	if len(cfg.Collections) != 1 {
		t.Fatalf("expected 1 collection after first request, got %d", len(cfg.Collections))
	}
	send()
	if len(cfg.Collections) != 1 {
		t.Errorf("expected still 1 collection after second request (no dup), got %d", len(cfg.Collections))
	}
}

func TestLazyInit_ConcurrentRequests_NoRace(t *testing.T) {
	cfg := &config.Config{}
	h := newLazyHandler(cfg, nil)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Waystones-OapifGo-B64", validB64)
			h.ServeHTTP(httptest.NewRecorder(), req)
		}()
	}
	wg.Wait()

	if len(cfg.Collections) != 1 {
		t.Errorf("expected exactly 1 collection after concurrent init, got %d", len(cfg.Collections))
	}
}
