package db

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/duckdb/duckdb-go/v2"

	"github.com/waystones/oapif-go/internal/config"
)

type Store struct {
	db *sql.DB
}

// Open opens an in-memory DuckDB database and loads the spatial and httpfs extensions.
//
// SetMaxOpenConns(1) is intentional for this spike: DuckDB's LOAD command is
// session-scoped, so using a single connection guarantees the extensions loaded here
// are the ones used by all queries. Concurrency is serialized, which is acceptable
// for measuring cold-start time. A production implementation would use duckdb.NewConnector
// to run extension setup on every connection in the pool.
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

// ConfigureR2 creates a DuckDB S3 secret for R2 access.
// Secrets are database-scoped in DuckDB, so one call is sufficient.
func (s *Store) ConfigureR2(ctx context.Context, cfg *config.Config) error {
	if cfg.R2AccessKey == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, fmt.Sprintf(`
		CREATE OR REPLACE SECRET r2 (
			TYPE s3,
			KEY_ID '%s',
			SECRET '%s',
			ENDPOINT '%s',
			REGION 'auto',
			URL_STYLE 'path'
		)`, cfg.R2AccessKey, cfg.R2SecretKey, cfg.R2Host))
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}

func parquetURL(bucket, key string) string {
	return fmt.Sprintf("s3://%s/%s", bucket, key)
}
