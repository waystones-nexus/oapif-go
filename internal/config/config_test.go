package config_test

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/waystones/oapif-go/internal/config"
)

// ---------------------------------------------------------------------------
// ApplyB64
// ---------------------------------------------------------------------------

func b64Encode(v interface{}) string {
	data, _ := json.Marshal(v)
	return base64.StdEncoding.EncodeToString(data)
}

func TestApplyB64_SingleCollection(t *testing.T) {
	payload := b64Encode(map[string]interface{}{
		"collections": []map[string]interface{}{
			{"id": "rivers", "parquet_key": "geo/rivers.parquet"},
		},
	})
	cfg := &config.Config{}
	if err := config.ApplyB64(cfg, payload); err != nil {
		t.Fatalf("ApplyB64 error: %v", err)
	}
	if len(cfg.Collections) != 1 {
		t.Fatalf("expected 1 collection, got %d", len(cfg.Collections))
	}
	if cfg.Collections[0].ID != "rivers" {
		t.Errorf("expected ID 'rivers', got %q", cfg.Collections[0].ID)
	}
}

func TestApplyB64_DefaultsFilled(t *testing.T) {
	// A collection with no geom_column, id_column, or crs should get defaults.
	payload := b64Encode(map[string]interface{}{
		"collections": []map[string]interface{}{
			{"id": "spots", "parquet_key": "spots.parquet"},
		},
	})
	cfg := &config.Config{}
	if err := config.ApplyB64(cfg, payload); err != nil {
		t.Fatal(err)
	}
	c := cfg.Collections[0]
	if c.GeomColumn != "geometry" {
		t.Errorf("GeomColumn: got %q, want 'geometry'", c.GeomColumn)
	}
	if c.IDColumn != "fid" {
		t.Errorf("IDColumn: got %q, want 'fid'", c.IDColumn)
	}
}

func TestApplyB64_NormalizeCRS(t *testing.T) {
	payload := b64Encode(map[string]interface{}{
		"collections": []map[string]interface{}{
			{
				"id":            "roads",
				"parquet_key":   "roads.parquet",
				"supported_crs": []string{"EPSG:3857"},
			},
		},
	})
	cfg := &config.Config{}
	if err := config.ApplyB64(cfg, payload); err != nil {
		t.Fatal(err)
	}
	got := cfg.Collections[0].SupportedCRS
	want := "http://www.opengis.net/def/crs/EPSG/0/3857"
	if len(got) != 1 || got[0] != want {
		t.Errorf("SupportedCRS: got %v, want [%s]", got, want)
	}
}

func TestApplyB64_ServerMetadata(t *testing.T) {
	payload := b64Encode(map[string]interface{}{
		"title":       "My API",
		"description": "A description",
		"collections": []map[string]interface{}{
			{"id": "x", "parquet_key": "x.parquet"},
		},
	})
	cfg := &config.Config{}
	if err := config.ApplyB64(cfg, payload); err != nil {
		t.Fatal(err)
	}
	if cfg.ServerTitle != "My API" {
		t.Errorf("ServerTitle: got %q, want 'My API'", cfg.ServerTitle)
	}
	if cfg.ServerDescription != "A description" {
		t.Errorf("ServerDescription: got %q", cfg.ServerDescription)
	}
}

func TestApplyB64_InvalidBase64(t *testing.T) {
	cfg := &config.Config{}
	if err := config.ApplyB64(cfg, "not-valid-base64!!!"); err == nil {
		t.Error("expected error for invalid base64, got nil")
	}
}

func TestApplyB64_InvalidJSON(t *testing.T) {
	cfg := &config.Config{}
	bad := base64.StdEncoding.EncodeToString([]byte("{invalid json"))
	if err := config.ApplyB64(cfg, bad); err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// ---------------------------------------------------------------------------
// CollectionByID
// ---------------------------------------------------------------------------

func TestCollectionByID_Found(t *testing.T) {
	cfg := &config.Config{
		Collections: []config.CollectionConfig{
			{ID: "alpha"},
			{ID: "beta"},
		},
	}
	c := cfg.CollectionByID("beta")
	if c == nil {
		t.Fatal("expected non-nil collection for 'beta'")
	}
	if c.ID != "beta" {
		t.Errorf("got ID %q, want 'beta'", c.ID)
	}
}

func TestCollectionByID_NotFound(t *testing.T) {
	cfg := &config.Config{
		Collections: []config.CollectionConfig{{ID: "alpha"}},
	}
	if c := cfg.CollectionByID("missing"); c != nil {
		t.Errorf("expected nil for unknown ID, got %v", c)
	}
}

func TestCollectionByID_Empty(t *testing.T) {
	cfg := &config.Config{}
	if c := cfg.CollectionByID("anything"); c != nil {
		t.Error("expected nil for empty collection list")
	}
}

// ---------------------------------------------------------------------------
// Load: S3 endpoint scheme -> S3Host / S3UseSSL
//
// DuckDB's S3 secret defaults USE_SSL to true when the parameter is omitted.
// Every query against a plain-HTTP endpoint (the local MinIO instance every
// docker-compose/Codespaces kit runs, AWS_ENDPOINT_URL=http://minio:9000) then
// fails at the TLS layer — surfaced by handlers.go as nothing more specific
// than a generic "query failed", indistinguishable from a real query bug.
// ---------------------------------------------------------------------------

func clearS3Env(t *testing.T) {
	t.Helper()
	for _, k := range []string{"S3_ENDPOINT", "AWS_ENDPOINT_URL", "R2_ENDPOINT", "S3_REGION", "S3_URL_STYLE"} {
		t.Setenv(k, "")
	}
}

func TestLoad_HTTPEndpointDisablesSSL(t *testing.T) {
	clearS3Env(t)
	t.Setenv("AWS_ENDPOINT_URL", "http://minio:9000")

	cfg := config.Load()

	if cfg.S3Host != "minio:9000" {
		t.Errorf("S3Host = %q, want %q", cfg.S3Host, "minio:9000")
	}
	if cfg.S3UseSSL {
		t.Error("S3UseSSL = true for an http:// endpoint, want false")
	}
}

func TestLoad_HTTPSEndpointKeepsSSLEnabled(t *testing.T) {
	clearS3Env(t)
	t.Setenv("AWS_ENDPOINT_URL", "https://abc123.r2.cloudflarestorage.com")

	cfg := config.Load()

	if !cfg.S3UseSSL {
		t.Error("S3UseSSL = false for an https:// endpoint, want true")
	}
}

func TestLoad_NoEndpointKeepsSSLEnabled(t *testing.T) {
	clearS3Env(t)

	cfg := config.Load()

	if cfg.S3Host != "" {
		t.Errorf("S3Host = %q, want empty (plain AWS S3, no custom endpoint)", cfg.S3Host)
	}
	if !cfg.S3UseSSL {
		t.Error("S3UseSSL = false with no custom endpoint set, want true (matches DuckDB's own default)")
	}
}
