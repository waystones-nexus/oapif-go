//go:build integration

package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/waystones/oapif-go/internal/config"
)

// parquetFixturePath returns the absolute path to the testdata parquet fixture,
// resolving relative to this source file so it works regardless of test working dir.
func parquetFixturePath() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", "testdata", "points.parquet")
}

// createFixture creates a small GeoParquet file at dst using DuckDB.
// 10 point features with fid(1–10), WKB geometry, name, value, ts columns.
func createFixture(ctx context.Context, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	db, err := sql.Open("duckdb", "")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	for _, stmt := range []string{"LOAD spatial"} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("init %q: %w", stmt, err)
		}
	}

	query := fmt.Sprintf(`
		COPY (
			SELECT
				CAST(i AS INTEGER) AS fid,
				ST_AsWKB(ST_Point(
					CAST(i AS DOUBLE) * 10.0 - 45.0,
					CAST(i AS DOUBLE) * 5.0 - 25.0
				)) AS geometry,
				'item-' || CAST(i AS VARCHAR) AS name,
				CAST(i AS INTEGER) * 100 AS value,
				TIMESTAMPTZ '2026-01-01 00:00:00+00' + INTERVAL (CAST(i AS INTEGER)) DAY AS ts
			FROM range(1, 11) t(i)
		) TO '%s' (FORMAT PARQUET)
	`, dst)

	_, err = db.ExecContext(ctx, query)
	return err
}

// testCollection returns a CollectionConfig pointing at the local fixture.
// bucket is left empty so parquetURL returns the key as a bare path.
func testCollection(fixturePath string) *config.CollectionConfig {
	return &config.CollectionConfig{
		ID:         "test",
		ParquetKey: fixturePath,
		GeomColumn: "geometry",
		IDColumn:   "fid",
	}
}

// TestMain creates the GeoParquet fixture once before all integration tests run.
func TestMain(m *testing.M) {
	ctx := context.Background()
	fix := parquetFixturePath()
	if _, err := os.Stat(fix); os.IsNotExist(err) {
		if err := createFixture(ctx, fix); err != nil {
			fmt.Fprintf(os.Stderr, "SKIP: could not create parquet fixture: %v\n", err)
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// DetectColumns
// ---------------------------------------------------------------------------

func TestDetectColumns(t *testing.T) {
	ctx := context.Background()
	store, err := openForTest(ctx)
	if err != nil {
		t.Skipf("DuckDB not available: %v", err)
	}
	defer store.Close()

	col := testCollection(parquetFixturePath())
	if err := store.DetectColumns(ctx, col, ""); err != nil {
		t.Fatalf("DetectColumns: %v", err)
	}
	if col.GeomColumn == "" {
		t.Error("GeomColumn should be set after DetectColumns")
	}
	if col.IDColumn == "" {
		t.Error("IDColumn should be set after DetectColumns")
	}
}

// ---------------------------------------------------------------------------
// CacheExtent
// ---------------------------------------------------------------------------

func TestCacheExtent(t *testing.T) {
	ctx := context.Background()
	store, err := openForTest(ctx)
	if err != nil {
		t.Skipf("DuckDB not available: %v", err)
	}
	defer store.Close()

	col := testCollection(parquetFixturePath())
	// Detect columns first so geom/id columns are resolved.
	if err := store.DetectColumns(ctx, col, ""); err != nil {
		t.Fatalf("DetectColumns: %v", err)
	}
	if err := store.CacheExtent(ctx, col, ""); err != nil {
		t.Fatalf("CacheExtent: %v", err)
	}
	e := col.Extent
	if e[0] == 0 && e[1] == 0 && e[2] == 0 && e[3] == 0 {
		t.Error("expected non-zero extent after CacheExtent")
	}
}

// ---------------------------------------------------------------------------
// CacheQueryables
// ---------------------------------------------------------------------------

func TestCacheQueryables(t *testing.T) {
	ctx := context.Background()
	store, err := openForTest(ctx)
	if err != nil {
		t.Skipf("DuckDB not available: %v", err)
	}
	defer store.Close()

	col := testCollection(parquetFixturePath())
	if err := store.DetectColumns(ctx, col, ""); err != nil {
		t.Fatalf("DetectColumns: %v", err)
	}
	if err := store.CacheQueryables(ctx, col, ""); err != nil {
		t.Fatalf("CacheQueryables: %v", err)
	}
	if len(col.Queryables) == 0 {
		t.Error("expected non-empty Queryables after CacheQueryables")
	}
	// "name" should be string, "value" should be integer.
	if q, ok := col.Queryables["name"]; !ok || q.Type != "string" {
		t.Errorf("expected name→string, got %+v", col.Queryables["name"])
	}
	if q, ok := col.Queryables["value"]; !ok || q.Type != "integer" {
		t.Errorf("expected value→integer, got %+v", col.Queryables["value"])
	}
}

// ---------------------------------------------------------------------------
// QueryItems
// ---------------------------------------------------------------------------

func setupStore(t *testing.T) (*Store, *config.CollectionConfig) {
	t.Helper()
	ctx := context.Background()
	store, err := openForTest(ctx)
	if err != nil {
		t.Skipf("DuckDB not available: %v", err)
	}
	col := testCollection(parquetFixturePath())
	if err := store.DetectColumns(ctx, col, ""); err != nil {
		store.Close()
		t.Fatalf("DetectColumns: %v", err)
	}
	if err := store.CacheQueryables(ctx, col, ""); err != nil {
		store.Close()
		t.Fatalf("CacheQueryables: %v", err)
	}
	return store, col
}

func TestQueryItems_NoFilter(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	features, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{Limit: 100})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 10 {
		t.Errorf("numberMatched: got %d, want 10", total)
	}
	if len(features) != 10 {
		t.Errorf("features returned: got %d, want 10", len(features))
	}
}

func TestQueryItems_Limit(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	features, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{Limit: 3})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 10 {
		t.Errorf("total: got %d, want 10", total)
	}
	if len(features) != 3 {
		t.Errorf("returned: got %d, want 3", len(features))
	}
}

func TestQueryItems_Offset(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	features, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{Limit: 100, Offset: 9})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 10 {
		t.Errorf("total: got %d, want 10", total)
	}
	if len(features) != 1 {
		t.Errorf("returned: got %d, want 1 (last feature)", len(features))
	}
}

func TestQueryItems_BboxAllFeatures(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	bbox := [4]float64{-180, -90, 180, 90}
	features, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{Limit: 100, Bbox: &bbox})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 10 {
		t.Errorf("bbox covering all: got total %d, want 10", total)
	}
	if len(features) != 10 {
		t.Errorf("bbox covering all: got %d features, want 10", len(features))
	}
}

func TestQueryItems_BboxSubset(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	// Points are at x = i*10-45, y = i*5-25 for i in 1..10.
	// x range: -35 (i=1) to 55 (i=10). bbox [0,0,180,90] catches i where x>0: i≥5 → 6 features (i=5..10).
	bbox := [4]float64{0, 0, 180, 90}
	_, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{Limit: 100, Bbox: &bbox})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total == 0 || total == 10 {
		t.Errorf("bbox subset: expected between 1 and 9, got %d", total)
	}
}

func TestQueryItems_DatetimeExact(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	// ts for i=3 is 2026-01-04 (Jan 1 + 3 days).
	ts := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	col.DatetimeColumn = "ts"
	_, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{
		Limit:    100,
		Datetime: &DatetimeFilter{Low: &ts, Exact: true},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 1 {
		t.Errorf("exact datetime: got %d, want 1", total)
	}
}

func TestQueryItems_DatetimeInterval(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	low := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)  // i=3
	high := time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC) // i=6
	col.DatetimeColumn = "ts"
	_, total, err := store.QueryItems(context.Background(), col, "", QueryOptions{
		Limit:    100,
		Datetime: &DatetimeFilter{Low: &low, High: &high},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if total != 4 {
		t.Errorf("interval datetime: got %d, want 4 (i=3,4,5,6)", total)
	}
}

func TestQueryItems_SortDesc(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	features, _, err := store.QueryItems(context.Background(), col, "", QueryOptions{
		Limit:  10,
		SortBy: []SortField{{Column: "value", Desc: true}},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(features) < 2 {
		t.Skip("not enough features to verify sort order")
	}
	// First feature should have the highest value (i=10 → value=1000).
	first, _ := features[0].Properties["value"].(int64)
	last, _ := features[len(features)-1].Properties["value"].(int64)
	if first < last {
		t.Errorf("expected descending sort, first value %d < last value %d", first, last)
	}
}

func TestQueryItems_Properties(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	features, _, err := store.QueryItems(context.Background(), col, "", QueryOptions{
		Limit:      10,
		Properties: []string{"name"},
	})
	if err != nil {
		t.Fatalf("QueryItems: %v", err)
	}
	if len(features) == 0 {
		t.Fatal("no features returned")
	}
	if _, ok := features[0].Properties["name"]; !ok {
		t.Error("expected 'name' property to be present")
	}
	if _, ok := features[0].Properties["value"]; ok {
		t.Error("expected 'value' property to be absent when not in properties list")
	}
}

// ---------------------------------------------------------------------------
// QueryItem
// ---------------------------------------------------------------------------

func TestQueryItem_Found(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	f, err := store.QueryItem(context.Background(), col, "", "5", "", nil)
	if err != nil {
		t.Fatalf("QueryItem: %v", err)
	}
	if f == nil {
		t.Fatal("expected non-nil feature for fid=5")
	}
	if name, _ := f.Properties["name"].(string); name != "item-5" {
		t.Errorf("expected name 'item-5', got %q", name)
	}
}

func TestQueryItem_NotFound(t *testing.T) {
	store, col := setupStore(t)
	defer store.Close()

	f, err := store.QueryItem(context.Background(), col, "", "9999", "", nil)
	if err != nil {
		t.Fatalf("QueryItem returned unexpected error: %v", err)
	}
	if f != nil {
		t.Error("expected nil feature for non-existent ID")
	}
}
