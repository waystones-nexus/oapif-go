package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waystones/oapif-go/internal/api"
	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/db"
)

// ---------------------------------------------------------------------------
// Mock store
// ---------------------------------------------------------------------------

type mockStore struct {
	items       []db.Feature
	itemTotal   int64
	itemErr     error
	single      *db.Feature
	singleErr   error
	prevID      string
	nextID      string
	adjacentErr error
	// capturedOpts records the last QueryOptions passed to QueryItems.
	capturedOpts *db.QueryOptions
}

func (m *mockStore) QueryItems(_ context.Context, _ *config.CollectionConfig, _ string, opts db.QueryOptions) ([]db.Feature, int64, error) {
	if m.capturedOpts != nil {
		*m.capturedOpts = opts
	}
	return m.items, m.itemTotal, m.itemErr
}

func (m *mockStore) QueryItem(_ context.Context, _ *config.CollectionConfig, _, _, _ string, _ []string) (*db.Feature, error) {
	return m.single, m.singleErr
}

func (m *mockStore) QueryAdjacentIDs(_ context.Context, _ *config.CollectionConfig, _, _ string) (string, string, error) {
	return m.prevID, m.nextID, m.adjacentErr
}

// ---------------------------------------------------------------------------
// Test server builder
// ---------------------------------------------------------------------------

func newTestServer(t *testing.T, cfg *config.Config, store db.Querier) *httptest.Server {
	t.Helper()
	dbReady := make(chan struct{})
	close(dbReady)
	h := api.NewHandler(cfg, dbReady, time.Now())
	h.SetQuerier(store)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.LandingPage)
	mux.HandleFunc("GET /conformance", h.Conformance)
	mux.HandleFunc("GET /queryables", h.GlobalQueryables)
	mux.HandleFunc("GET /collections", h.Collections)
	mux.HandleFunc("GET /collections/{collectionId}", h.Collection)
	mux.HandleFunc("GET /collections/{collectionId}/queryables", h.Queryables)
	mux.HandleFunc("GET /collections/{collectionId}/items", h.Items)
	mux.HandleFunc("GET /collections/{collectionId}/items/{featureId}", h.Item)
	mux.HandleFunc("GET /api", h.OpenAPI)
	// /health needs a separate Handler because it checks h.dbReady directly.
	mux.HandleFunc("GET /health", h.Health)

	return httptest.NewServer(mux)
}

func testCfg() *config.Config {
	return &config.Config{
		ServerURL:   "http://localhost",
		ServerTitle: "Test API",
		S3Bucket:    "test-bucket",
		Collections: []config.CollectionConfig{
			{
				ID:         "places",
				Title:      "Places",
				ParquetKey: "places.parquet",
				GeomColumn: "geometry",
				IDColumn:   "fid",
				Queryables: map[string]config.QueryableField{
					"name":  {Type: "string"},
					"value": {Type: "integer"},
				},
			},
		},
		CORSAllowedOrigins: []string{"*"},
	}
}

func getJSON(t *testing.T, srv *httptest.Server, path string) (int, map[string]interface{}) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]interface{}
	json.Unmarshal(body, &out) //nolint:errcheck
	return resp.StatusCode, out
}

func getRaw(t *testing.T, srv *httptest.Server, path string) (int, http.Header, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, body
}

// ---------------------------------------------------------------------------
// /health
// ---------------------------------------------------------------------------

func TestHealth_Ready(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/health")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if body["status"] != "ok" {
		t.Errorf("expected status 'ok', got %v", body["status"])
	}
}

// ---------------------------------------------------------------------------
// / (LandingPage)
// ---------------------------------------------------------------------------

func TestLandingPage_JSON(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if _, ok := body["links"]; !ok {
		t.Error("LandingPage response missing 'links' key")
	}
}

func TestLandingPage_HTML(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("HTML request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for HTML, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// /conformance
// ---------------------------------------------------------------------------

func TestConformance(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/conformance")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	conforms, ok := body["conformsTo"].([]interface{})
	if !ok || len(conforms) == 0 {
		t.Error("expected non-empty conformsTo array")
	}
}

// ---------------------------------------------------------------------------
// /collections
// ---------------------------------------------------------------------------

func TestCollections_List(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/collections")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	cols, ok := body["collections"].([]interface{})
	if !ok || len(cols) != 1 {
		t.Errorf("expected 1 collection, got %v", body["collections"])
	}
}

// ---------------------------------------------------------------------------
// /collections/{id}
// ---------------------------------------------------------------------------

func TestCollection_Found(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/collections/places")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if body["id"] != "places" {
		t.Errorf("expected id 'places', got %v", body["id"])
	}
}

func TestCollection_NotFound(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, _ := getJSON(t, srv, "/collections/nonexistent")
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// /collections/{id}/queryables
// ---------------------------------------------------------------------------

func TestQueryables_Found(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/collections/places/queryables")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	props, ok := body["properties"].(map[string]interface{})
	if !ok || len(props) == 0 {
		t.Error("expected non-empty properties in queryables response")
	}
}

func TestQueryables_UnknownCollection(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, _ := getJSON(t, srv, "/collections/unknown/queryables")
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

// ---------------------------------------------------------------------------
// /collections/{id}/items
// ---------------------------------------------------------------------------

func makeFeatures(n int) []db.Feature {
	out := make([]db.Feature, n)
	for i := range out {
		out[i] = db.Feature{
			ID:         i + 1,
			Geometry:   json.RawMessage(`{"type":"Point","coordinates":[0,0]}`),
			Properties: map[string]interface{}{"name": "feature"},
		}
	}
	return out
}

func TestItems_Basic(t *testing.T) {
	store := &mockStore{items: makeFeatures(3), itemTotal: 10}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, body := getJSON(t, srv, "/collections/places/items?limit=3")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if body["numberMatched"] != float64(10) {
		t.Errorf("numberMatched: got %v, want 10", body["numberMatched"])
	}
	if body["numberReturned"] != float64(3) {
		t.Errorf("numberReturned: got %v, want 3", body["numberReturned"])
	}
}

func TestItems_NextLink_Present(t *testing.T) {
	// 3 returned out of 10 total → next link expected
	store := &mockStore{items: makeFeatures(3), itemTotal: 10}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, body := getJSON(t, srv, "/collections/places/items?limit=3&offset=0")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	links, _ := body["links"].([]interface{})
	hasNext := false
	for _, l := range links {
		if lm, ok := l.(map[string]interface{}); ok && lm["rel"] == "next" {
			hasNext = true
		}
	}
	if !hasNext {
		t.Error("expected a 'next' link when more results are available")
	}
}

func TestItems_PrevLink_AtOffset(t *testing.T) {
	store := &mockStore{items: makeFeatures(3), itemTotal: 10}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	_, body := getJSON(t, srv, "/collections/places/items?limit=3&offset=5")
	links, _ := body["links"].([]interface{})
	hasPrev := false
	for _, l := range links {
		if lm, ok := l.(map[string]interface{}); ok && lm["rel"] == "prev" {
			hasPrev = true
		}
	}
	if !hasPrev {
		t.Error("expected a 'prev' link when offset > 0")
	}
}

func TestItems_CacheControlHeader(t *testing.T) {
	store := &mockStore{items: makeFeatures(1), itemTotal: 1}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, hdr, _ := getRaw(t, srv, "/collections/places/items")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	cc := hdr.Get("Cache-Control")
	if cc == "" {
		t.Error("expected Cache-Control header to be set on /items")
	}
}

func TestItems_UnknownCollection(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, _ := getJSON(t, srv, "/collections/missing/items")
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestItems_DBError(t *testing.T) {
	store := &mockStore{itemErr: context.DeadlineExceeded}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()
	// DeadlineExceeded from QueryItems → 500
	code, _ := getJSON(t, srv, "/collections/places/items")
	if code != http.StatusInternalServerError {
		t.Errorf("expected 500 on DB error, got %d", code)
	}
}

func TestItems_BboxParam(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?bbox=-10,-20,10,20")
	if captured.Bbox == nil {
		t.Error("expected Bbox to be populated in QueryOptions")
	}
}

func TestItems_DatetimeParam(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?datetime=2026-01-01T00:00:00Z")
	if captured.Datetime == nil || !captured.Datetime.Exact {
		t.Error("expected Datetime.Exact=true in QueryOptions")
	}
}

func TestItems_CQLFilter(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?filter=name+%3D+%27foo%27&filter-lang=cql2-text")
	if captured.Filter == nil {
		t.Error("expected Filter to be set in QueryOptions")
	}
}

func TestItems_OutputCRS(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?crs=http://www.opengis.net/def/crs/EPSG/0/3857")
	if captured.OutputCRS != "EPSG:3857" {
		t.Errorf("expected OutputCRS 'EPSG:3857', got %q", captured.OutputCRS)
	}
}

func TestItems_SortByParam(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?sortby=-name")
	if len(captured.SortBy) != 1 || captured.SortBy[0].Column != "name" || !captured.SortBy[0].Desc {
		t.Errorf("unexpected SortBy: %+v", captured.SortBy)
	}
}

func TestItems_PropertiesParam(t *testing.T) {
	var captured db.QueryOptions
	store := &mockStore{items: makeFeatures(1), itemTotal: 1, capturedOpts: &captured}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	getJSON(t, srv, "/collections/places/items?properties=name")
	if len(captured.Properties) != 1 || captured.Properties[0] != "name" {
		t.Errorf("unexpected Properties: %v", captured.Properties)
	}
}

// ---------------------------------------------------------------------------
// /collections/{id}/items/{featureId}
// ---------------------------------------------------------------------------

func TestItem_Found(t *testing.T) {
	f := &db.Feature{
		ID:         42,
		Geometry:   json.RawMessage(`{"type":"Point","coordinates":[1,2]}`),
		Properties: map[string]interface{}{"name": "test"},
	}
	store := &mockStore{single: f}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, body := getJSON(t, srv, "/collections/places/items/42")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if body["type"] != "Feature" {
		t.Errorf("expected type 'Feature', got %v", body["type"])
	}
}

func TestItem_NotFound(t *testing.T) {
	store := &mockStore{single: nil, singleErr: nil}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, _ := getJSON(t, srv, "/collections/places/items/9999")
	if code != http.StatusNotFound {
		t.Errorf("expected 404 for missing feature, got %d", code)
	}
}

func TestItem_UnknownCollection(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, _ := getJSON(t, srv, "/collections/missing/items/1")
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestItem_CacheControlHeader(t *testing.T) {
	f := &db.Feature{ID: 1, Geometry: json.RawMessage(`null`), Properties: map[string]interface{}{}}
	store := &mockStore{single: f}
	srv := newTestServer(t, testCfg(), store)
	defer srv.Close()

	code, hdr, _ := getRaw(t, srv, "/collections/places/items/1")
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d", code)
	}
	if hdr.Get("Cache-Control") == "" {
		t.Error("expected Cache-Control header on /items/{id}")
	}
}

// ---------------------------------------------------------------------------
// /api
// ---------------------------------------------------------------------------

func TestOpenAPI(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()
	code, body := getJSON(t, srv, "/api")
	if code != http.StatusOK {
		t.Errorf("expected 200, got %d", code)
	}
	if _, ok := body["paths"]; !ok {
		t.Error("OpenAPI response missing 'paths' key")
	}
}

// ---------------------------------------------------------------------------
// Conditional requests (304 Not Modified)
// ---------------------------------------------------------------------------

func TestConditional_ETagMatch(t *testing.T) {
	srv := newTestServer(t, testCfg(), &mockStore{})
	defer srv.Close()

	// First request to get ETag
	resp1, err := http.Get(srv.URL + "/collections")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	etag := resp1.Header.Get("ETag")
	if etag == "" {
		t.Skip("server returned no ETag — skipping conditional test")
	}

	// Second request with If-None-Match
	req, _ := http.NewRequest("GET", srv.URL+"/collections", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err2 := http.DefaultClient.Do(req)
	if err2 != nil {
		t.Fatalf("second request: %v", err2)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("expected 304, got %d", resp2.StatusCode)
	}
}
