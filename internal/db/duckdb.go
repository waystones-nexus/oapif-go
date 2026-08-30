package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/waystones/oapif-go/internal/config"
)

type Store struct {
	db *sql.DB
}

// Open opens an in-memory DuckDB database and loads the spatial and httpfs extensions.
//
// SetMaxOpenConns(1) is intentional for this spike: DuckDB's LOAD command is
// session-scoped, so a single connection guarantees the loaded extensions are used by
// all queries. A production implementation would use duckdb.NewConnector to run
// extension setup on every connection in the pool.
func Open(ctx context.Context) (*Store, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	for _, stmt := range []string{
		"SET extension_directory='/extensions'",
		"LOAD httpfs",
		"LOAD spatial",
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("duckdb init %q: %w", stmt, err)
		}
	}

	return &Store{db: db}, nil
}

// buildS3SecretSQL builds the CREATE SECRET statement ConfigureS3 executes.
// Split out from ConfigureS3 so the generated SQL — in particular, whether
// USE_SSL ends up in it — is unit-testable without a live DuckDB connection.
//
// Credential values are single-quote-escaped before SQL interpolation to prevent
// malformed SQL if a key or secret contains a quote character.
func buildS3SecretSQL(cfg *config.Config) string {
	escSQ := func(v string) string { return strings.ReplaceAll(v, "'", "''") }

	var sb strings.Builder
	sb.WriteString("CREATE OR REPLACE SECRET s3creds (\n\tTYPE s3,\n")
	fmt.Fprintf(&sb, "\tKEY_ID '%s',\n", escSQ(cfg.S3AccessKey))
	fmt.Fprintf(&sb, "\tSECRET '%s',\n", escSQ(cfg.S3SecretKey))
	fmt.Fprintf(&sb, "\tREGION '%s'", escSQ(cfg.S3Region))

	if cfg.S3Host != "" {
		fmt.Fprintf(&sb, ",\n\tENDPOINT '%s'", escSQ(cfg.S3Host))
		// DuckDB's S3 secret defaults USE_SSL to true when this parameter is
		// omitted — every query against a plain-HTTP endpoint (the local MinIO
		// instance every docker-compose/Codespaces kit runs,
		// AWS_ENDPOINT_URL=http://minio:9000) then fails at the TLS layer,
		// surfaced by handlers.go as nothing more specific than a generic
		// "query failed", indistinguishable from a real query bug.
		fmt.Fprintf(&sb, ",\n\tUSE_SSL %t", cfg.S3UseSSL)
	}
	if cfg.S3URLStyle != "" {
		fmt.Fprintf(&sb, ",\n\tURL_STYLE '%s'", escSQ(cfg.S3URLStyle))
	}
	sb.WriteString("\n)")
	return sb.String()
}

// ConfigureS3 creates a DuckDB S3 secret for any S3-compatible store.
// Works with AWS S3, Cloudflare R2, MinIO, or any other S3-compatible endpoint.
// Secrets are database-scoped in DuckDB so one call is sufficient.
func (s *Store) ConfigureS3(ctx context.Context, cfg *config.Config) error {
	if cfg.S3AccessKey == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, buildS3SecretSQL(cfg))
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}

func parquetURL(bucket, key string) string {
	if bucket == "" {
		return key // bare path for local files (used in integration tests)
	}
	return fmt.Sprintf("s3://%s/%s", bucket, key)
}
