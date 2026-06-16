//go:build integration

package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
)

// openForTest opens an in-memory DuckDB with the spatial extension loaded.
// httpfs is skipped since integration tests use local file paths, not S3.
// Set DUCKDB_EXTENSION_DIR to override the extension search directory
// (useful when running outside Docker where /extensions doesn't exist).
func openForTest(ctx context.Context) (*Store, error) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	stmts := []string{}
	if dir := os.Getenv("DUCKDB_EXTENSION_DIR"); dir != "" {
		stmts = append(stmts, fmt.Sprintf("SET extension_directory='%s'", dir))
	}
	stmts = append(stmts, "LOAD spatial")

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return nil, fmt.Errorf("duckdb init %q: %w", stmt, err)
		}
	}
	return &Store{db: db}, nil
}
