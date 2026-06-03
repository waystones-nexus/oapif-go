package api_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/waystones/oapif-go/internal/api"
)

// echoHandler writes the given body and status to the response.
func echoHandler(status int, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		io.WriteString(w, body) //nolint:errcheck
	})
}

// ---------------------------------------------------------------------------
// CORS
// ---------------------------------------------------------------------------

func TestCORSWildcard(t *testing.T) {
	h := api.CORSMiddleware([]string{"*"})(echoHandler(200, "ok"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://example.com")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("ACAO = %q, want *", got)
	}
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestCORSSpecificOriginMatch(t *testing.T) {
	h := api.CORSMiddleware([]string{"https://a.com", "https://b.com"})(echoHandler(200, "ok"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://b.com")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://b.com" {
		t.Errorf("ACAO = %q, want https://b.com", got)
	}
}

func TestCORSSpecificOriginNoMatch(t *testing.T) {
	h := api.CORSMiddleware([]string{"https://a.com"})(echoHandler(200, "ok"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Origin", "https://other.com")
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("ACAO = %q, want empty", got)
	}
}

func TestCORSOptionsPreflightReturns204(t *testing.T) {
	h := api.CORSMiddleware([]string{"*"})(echoHandler(200, "should not reach"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("OPTIONS", "/collections", nil)
	req.Header.Set("Origin", "https://example.com")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("OPTIONS body should be empty, got %q", rec.Body.String())
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("Allow-Methods missing GET: %q", got)
	}
}

// ---------------------------------------------------------------------------
// HEAD
// ---------------------------------------------------------------------------

func TestHEADStripsBody(t *testing.T) {
	h := api.HeadMiddleware(echoHandler(200, "this body must not appear"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("HEAD", "/", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body not empty: %q", rec.Body.String())
	}
}

func TestHEADPreservesHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "yes")
		w.WriteHeader(200)
		io.WriteString(w, "body") //nolint:errcheck
	})
	h := api.HeadMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("HEAD", "/", nil)
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("X-Custom"); got != "yes" {
		t.Errorf("X-Custom = %q, want yes", got)
	}
}

// ---------------------------------------------------------------------------
// Gzip
// ---------------------------------------------------------------------------

// bigBody returns a string longer than 1 KB.
func bigBody() string { return strings.Repeat("x", 1100) }

func TestGzipCompressesLargeResponse(t *testing.T) {
	h := api.GzipMiddleware(echoHandler(200, bigBody()))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	// Decompress and verify content.
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	body, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if string(body) != bigBody() {
		t.Errorf("decompressed body mismatch (len=%d)", len(body))
	}
}

func TestGzipSkipsSmallResponse(t *testing.T) {
	h := api.GzipMiddleware(echoHandler(200, "small"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty for small response", got)
	}
	if rec.Body.String() != "small" {
		t.Errorf("body = %q, want small", rec.Body.String())
	}
}

func TestGzipSkipsWhenClientNoAccept(t *testing.T) {
	h := api.GzipMiddleware(echoHandler(200, bigBody()))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	// No Accept-Encoding header.
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want empty when client has no Accept-Encoding", got)
	}
}

func TestGzipAddsVaryAlways(t *testing.T) {
	h := api.GzipMiddleware(echoHandler(200, "any"))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	// No Accept-Encoding
	h.ServeHTTP(rec, req)
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Accept-Encoding") {
		t.Errorf("Vary = %q, want to contain Accept-Encoding", got)
	}
}

func TestGzipPreservesStatusCode(t *testing.T) {
	h := api.GzipMiddleware(echoHandler(404, bigBody()))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// f-parameter
// ---------------------------------------------------------------------------

func TestFParamJSON(t *testing.T) {
	var gotAccept string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(200)
	})
	h := api.FParamMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?f=json", nil)
	h.ServeHTTP(rec, req)
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
}

func TestFParamGeoJSON(t *testing.T) {
	var gotAccept string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(200)
	})
	h := api.FParamMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?f=geojson", nil)
	h.ServeHTTP(rec, req)
	if gotAccept != "application/geo+json" {
		t.Errorf("Accept = %q, want application/geo+json", gotAccept)
	}
}

func TestFParamHTML(t *testing.T) {
	var gotAccept string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		w.WriteHeader(200)
	})
	h := api.FParamMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?f=html", nil)
	h.ServeHTTP(rec, req)
	if gotAccept != "text/html" {
		t.Errorf("Accept = %q, want text/html", gotAccept)
	}
}

func TestFParamInvalidReturns400(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("inner handler should not be called for invalid f")
	})
	h := api.FParamMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/?f=xml", nil)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestFParamAbsentPassesThrough(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		// Accept header should be unchanged (empty in this test).
		if got := r.Header.Get("Accept"); got != "" {
			t.Errorf("Accept = %q, want empty", got)
		}
		w.WriteHeader(200)
	})
	h := api.FParamMiddleware(inner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/collections", nil)
	h.ServeHTTP(rec, req)
	if !called {
		t.Error("inner handler was not called")
	}
}
