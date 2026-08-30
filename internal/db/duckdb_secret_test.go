package db

import (
	"strings"
	"testing"

	"github.com/waystones/oapif-go/internal/config"
)

// Unlike duckdb_test.go / queries_test.go, this file needs no live DuckDB
// connection — buildS3SecretSQL is pure string building — so it has no
// "integration" build tag and runs under a plain `go test ./...`.

func TestBuildS3SecretSQL_HTTPEndpointEmitsUseSSLFalse(t *testing.T) {
	cfg := &config.Config{
		S3AccessKey: "minioadmin",
		S3SecretKey: "minioadmin",
		S3Region:    "auto",
		S3Host:      "minio:9000",
		S3UseSSL:    false,
		S3URLStyle:  "path",
	}

	sql := buildS3SecretSQL(cfg)

	if !strings.Contains(sql, "USE_SSL false") {
		t.Errorf("expected SQL to contain USE_SSL false for a plain-HTTP endpoint, got:\n%s", sql)
	}
}

func TestBuildS3SecretSQL_HTTPSEndpointEmitsUseSSLTrue(t *testing.T) {
	cfg := &config.Config{
		S3AccessKey: "key",
		S3SecretKey: "secret",
		S3Region:    "auto",
		S3Host:      "abc123.r2.cloudflarestorage.com",
		S3UseSSL:    true,
		S3URLStyle:  "path",
	}

	sql := buildS3SecretSQL(cfg)

	if !strings.Contains(sql, "USE_SSL true") {
		t.Errorf("expected SQL to contain USE_SSL true for an https:// endpoint, got:\n%s", sql)
	}
}

func TestBuildS3SecretSQL_NoHostOmitsEndpointAndUseSSL(t *testing.T) {
	// Plain AWS S3 (no custom endpoint) relies on DuckDB's own defaults —
	// USE_SSL true — rather than this code asserting it explicitly.
	cfg := &config.Config{
		S3AccessKey: "key",
		S3SecretKey: "secret",
		S3Region:    "us-east-1",
	}

	sql := buildS3SecretSQL(cfg)

	if strings.Contains(sql, "ENDPOINT") {
		t.Errorf("expected no ENDPOINT clause with no S3Host set, got:\n%s", sql)
	}
	if strings.Contains(sql, "USE_SSL") {
		t.Errorf("expected no USE_SSL clause with no S3Host set, got:\n%s", sql)
	}
}
