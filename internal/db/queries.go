package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/waystones/oapif-go/internal/config"
)

// CacheExtent computes and stores the spatial extent of the collection's parquet file.
// Called once at startup; extent is stored on the CollectionConfig pointer.
func (s *Store) CacheExtent(ctx context.Context, col *config.CollectionConfig, bucket string) error {
	purl := parquetURL(bucket, col.R2Key)
	geom := col.GeomColumn
	query := fmt.Sprintf(`
		SELECT
			MIN(ST_XMin(ST_Envelope(ST_GeomFromWKB(%s)))),
			MIN(ST_YMin(ST_Envelope(ST_GeomFromWKB(%s)))),
			MAX(ST_XMax(ST_Envelope(ST_GeomFromWKB(%s)))),
			MAX(ST_YMax(ST_Envelope(ST_GeomFromWKB(%s))))
		FROM read_parquet('%s')
	`, geom, geom, geom, geom, purl)

	row := s.db.QueryRowContext(ctx, query)
	var minX, minY, maxX, maxY sql.NullFloat64
	if err := row.Scan(&minX, &minY, &maxX, &maxY); err != nil {
		return err
	}
	// Default to world extent if the file is empty or extent is NULL
	if !minX.Valid {
		col.Extent = [4]float64{-180, -90, 180, 90}
	} else {
		col.Extent = [4]float64{minX.Float64, minY.Float64, maxX.Float64, maxY.Float64}
	}
	return nil
}

// CacheQueryables introspects the parquet schema and stores the field definitions.
// Called once at startup; queryables are stored on the CollectionConfig pointer.
func (s *Store) CacheQueryables(ctx context.Context, col *config.CollectionConfig, bucket string) error {
	purl := parquetURL(bucket, col.R2Key)
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		"DESCRIBE SELECT * FROM read_parquet('%s') LIMIT 0", purl,
	))
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	col.Queryables = make(map[string]config.QueryableField)
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		colName := fmt.Sprintf("%v", vals[0])
		colType := fmt.Sprintf("%v", vals[1])
		if colName == col.GeomColumn {
			continue
		}
		col.Queryables[colName] = duckTypeToSchema(colType)
	}
	return rows.Err()
}

func duckTypeToSchema(t string) config.QueryableField {
	t = strings.ToUpper(strings.TrimSpace(t))
	switch {
	case strings.HasPrefix(t, "VARCHAR"), strings.HasPrefix(t, "TEXT"), t == "STRING":
		return config.QueryableField{Type: "string"}
	case t == "INTEGER", t == "INT", t == "INT4", t == "SIGNED",
		t == "BIGINT", t == "INT8", t == "LONG",
		t == "HUGEINT", t == "UHUGEINT", t == "UBIGINT",
		t == "SMALLINT", t == "INT2", t == "SHORT",
		t == "TINYINT", t == "INT1", t == "UINTEGER", t == "USMALLINT", t == "UTINYINT":
		return config.QueryableField{Type: "integer"}
	case strings.HasPrefix(t, "DOUBLE"), strings.HasPrefix(t, "FLOAT"),
		strings.HasPrefix(t, "DECIMAL"), strings.HasPrefix(t, "NUMERIC"), t == "REAL":
		return config.QueryableField{Type: "number"}
	case t == "BOOLEAN", t == "BOOL", t == "LOGICAL":
		return config.QueryableField{Type: "boolean"}
	case strings.HasPrefix(t, "TIMESTAMP"), strings.HasPrefix(t, "DATE"):
		return config.QueryableField{Type: "string", Format: "date-time"}
	default:
		return config.QueryableField{Type: "string"}
	}
}

// Warmup runs a lightweight COUNT against each collection to prime DuckDB's R2 connection
// and parquet metadata cache before the HTTP server starts.
func (s *Store) Warmup(ctx context.Context, cols []config.CollectionConfig, bucket string) error {
	for _, col := range cols {
		var count int64
		err := s.db.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM read_parquet('%s') LIMIT 1", parquetURL(bucket, col.R2Key)),
		).Scan(&count)
		if err != nil {
			return fmt.Errorf("warmup %s: %w", col.ID, err)
		}
	}
	return nil
}

// Feature holds one GeoJSON feature returned from DuckDB.
type Feature struct {
	ID         interface{}
	Geometry   json.RawMessage
	Properties map[string]interface{}
}

// QueryItems fetches a paginated, optionally bbox-filtered list of features.
func (s *Store) QueryItems(ctx context.Context, col *config.CollectionConfig, bucket string, limit, offset int, bbox *[4]float64) ([]Feature, int64, error) {
	purl := parquetURL(bucket, col.R2Key)

	where := ""
	if bbox != nil {
		where = fmt.Sprintf(
			"WHERE ST_Intersects(ST_GeomFromWKB(%s), ST_MakeEnvelope(%f, %f, %f, %f))",
			col.GeomColumn, bbox[0], bbox[1], bbox[2], bbox[3],
		)
	}

	var total int64
	if err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM read_parquet('%s') %s", purl, where),
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count: %w", err)
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			ST_AsGeoJSON(ST_GeomFromWKB(%s))::VARCHAR AS geometry,
			* EXCLUDE (%s, %s)
		FROM read_parquet('%s')
		%s
		LIMIT %d OFFSET %d
	`, col.IDColumn, col.GeomColumn, col.GeomColumn, col.IDColumn, purl, where, limit, offset)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, 0, fmt.Errorf("items: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, 0, err
	}

	features := make([]Feature, 0)
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, 0, err
		}

		f := Feature{
			ID:         vals[0],
			Properties: make(map[string]interface{}, len(cols)-2),
		}
		f.Geometry = toRawJSON(vals[1])
		for i := 2; i < len(cols); i++ {
			f.Properties[cols[i]] = vals[i]
		}
		features = append(features, f)
	}
	return features, total, rows.Err()
}

// QueryItem fetches a single feature by its ID column value.
// Returns (nil, nil) when the feature is not found.
func (s *Store) QueryItem(ctx context.Context, col *config.CollectionConfig, bucket, featureID string) (*Feature, error) {
	purl := parquetURL(bucket, col.R2Key)
	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			ST_AsGeoJSON(ST_GeomFromWKB(%s))::VARCHAR AS geometry,
			* EXCLUDE (%s, %s)
		FROM read_parquet('%s')
		WHERE CAST(%s AS VARCHAR) = '%s'
		LIMIT 1
	`, col.IDColumn, col.GeomColumn, col.GeomColumn, col.IDColumn, purl, col.IDColumn, featureID)

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		return nil, rows.Err()
	}

	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return nil, err
	}

	f := &Feature{
		ID:         vals[0],
		Properties: make(map[string]interface{}, len(cols)-2),
	}
	f.Geometry = toRawJSON(vals[1])
	for i := 2; i < len(cols); i++ {
		f.Properties[cols[i]] = vals[i]
	}
	return f, rows.Err()
}

// toRawJSON converts a value returned by DuckDB (string or []byte) to json.RawMessage.
// DuckDB may return JSON columns as either type depending on driver version.
func toRawJSON(v interface{}) json.RawMessage {
	switch val := v.(type) {
	case string:
		return json.RawMessage(val)
	case []byte:
		return json.RawMessage(val)
	}
	return json.RawMessage("null")
}
