package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/waystones/oapif-go/internal/config"
)

// DatetimeFilter represents a parsed OGC API datetime parameter.
type DatetimeFilter struct {
	Low   *time.Time // nil = unbounded start; also used as exact value when Exact=true
	High  *time.Time // nil = unbounded end
	Exact bool       // if true, Low is an equality match
}

// DetectColumns reads the parquet schema and GeoParquet `geo` metadata to determine:
//   - geometry column name and whether it is a native GEOMETRY type or WKB BLOB
//   - ID column name
//   - whether pre-computed bbox columns exist (for row-group pruning)
//
// It mutates col in place; all subsequent queries use the resolved field names.
func (s *Store) DetectColumns(ctx context.Context, col *config.CollectionConfig, bucket string) error {
	purl := parquetURL(bucket, col.ParquetKey)

	// Read GeoParquet spec `geo` key-value metadata (parquet file footer, no row scan).
	type geoColMeta struct {
		Encoding string    `json:"encoding"`
		Bbox     []float64 `json:"bbox"`
	}
	type geoMeta struct {
		PrimaryColumn string                 `json:"primary_column"`
		Columns       map[string]geoColMeta  `json:"columns"`
	}
	var geo geoMeta
	var rawGeo interface{}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT value FROM parquet_kv_metadata('%s') WHERE key='geo'", purl,
	)).Scan(&rawGeo); err == nil {
		var b []byte
		switch v := rawGeo.(type) {
		case []byte:
			b = v
		case string:
			b = []byte(v)
		}
		json.Unmarshal(b, &geo) //nolint:errcheck — partial parse is fine
	}

	// Read parquet schema.
	descRows, err := s.db.QueryContext(ctx, fmt.Sprintf(
		"DESCRIBE SELECT * FROM read_parquet('%s') LIMIT 0", purl,
	))
	if err != nil {
		return err
	}
	defer descRows.Close()

	descCols, err := descRows.Columns()
	if err != nil {
		return err
	}

	type colMeta struct{ name, typ string }
	var schema []colMeta
	for descRows.Next() {
		vals := make([]interface{}, len(descCols))
		ptrs := make([]interface{}, len(descCols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := descRows.Scan(ptrs...); err != nil {
			return err
		}
		schema = append(schema, colMeta{
			name: fmt.Sprintf("%v", vals[0]),
			typ:  fmt.Sprintf("%v", vals[1]),
		})
	}
	if err := descRows.Err(); err != nil {
		return err
	}

	nameSet := make(map[string]string, len(schema)) // name → type
	for _, c := range schema {
		nameSet[c.name] = c.typ
	}

	// Resolve geometry column: configured name → GeoParquet primary_column → name heuristics → first BLOB.
	if _, ok := nameSet[col.GeomColumn]; !ok {
		if geo.PrimaryColumn != "" {
			if _, ok := nameSet[geo.PrimaryColumn]; ok {
				col.GeomColumn = geo.PrimaryColumn
			}
		}
		if _, ok := nameSet[col.GeomColumn]; !ok {
			for _, candidate := range []string{"geometry", "geom", "the_geom", "wkb_geometry", "shape"} {
				if _, ok := nameSet[candidate]; ok {
					col.GeomColumn = candidate
					break
				}
			}
		}
		if _, ok := nameSet[col.GeomColumn]; !ok {
			for _, c := range schema {
				if strings.ToUpper(c.typ) == "BLOB" {
					col.GeomColumn = c.name
					break
				}
			}
		}
	}

	// Resolve ID column: configured name → name heuristics → first INT → first non-geom.
	if _, ok := nameSet[col.IDColumn]; !ok {
		for _, candidate := range []string{"fid", "id", "gid", "osm_id", "objectid", "feature_id"} {
			if _, ok := nameSet[candidate]; ok {
				col.IDColumn = candidate
				break
			}
		}
		if _, ok := nameSet[col.IDColumn]; !ok {
			for _, c := range schema {
				if strings.Contains(strings.ToUpper(c.typ), "INT") {
					col.IDColumn = c.name
					break
				}
			}
		}
		if _, ok := nameSet[col.IDColumn]; !ok {
			for _, c := range schema {
				if c.name != col.GeomColumn {
					col.IDColumn = c.name
					break
				}
			}
		}
	}

	// Resolve GeomIsNative from DuckDB's DESCRIBE output (authoritative at query time).
	// DuckDB's spatial extension auto-converts GeoParquet WKB columns to native GEOMETRY,
	// so the GeoParquet encoding field does not reflect the actual runtime type.
	if t, ok := nameSet[col.GeomColumn]; ok {
		col.GeomIsNative = strings.HasPrefix(strings.ToUpper(t), "GEOMETRY")
	}

	// Detect pre-computed bbox columns for row-group pruning.
	lname := func(n string) string { return strings.ToLower(n) }
	lnames := make(map[string]bool, len(schema))
	for _, c := range schema {
		lnames[lname(c.name)] = true
	}
	switch {
	case lnames["bbox_xmin"] && lnames["bbox_ymin"] && lnames["bbox_xmax"] && lnames["bbox_ymax"]:
		col.BboxColsStyle = "flat"
	case lnames["bbox"]:
		col.BboxColsStyle = "struct"
	}

	// Detect datetime column: first TIMESTAMP/DATE column.
	if col.DatetimeColumn == "" {
		for _, c := range schema {
			t := strings.ToUpper(c.typ)
			if strings.HasPrefix(t, "TIMESTAMP") || strings.HasPrefix(t, "DATE") || t == "TIMESTAMPTZ" {
				col.DatetimeColumn = c.name
				break
			}
		}
	}

	return nil
}

// geomExpr returns the SQL expression that produces a DuckDB GEOMETRY value from the geometry column.
// Native GEOMETRY columns need no wrapper; WKB BLOB columns require ST_GeomFromWKB().
func geomExpr(col *config.CollectionConfig) string {
	if col.GeomIsNative {
		return col.GeomColumn
	}
	return fmt.Sprintf("ST_GeomFromWKB(%s)", col.GeomColumn)
}

// CacheExtent stores the spatial extent of the collection. It first tries reading the `bbox`
// array from the GeoParquet `geo` key-value metadata (instant, no row scan). Only if that
// is absent does it fall back to a full ST_Envelope scan over all rows.
func (s *Store) CacheExtent(ctx context.Context, col *config.CollectionConfig, bucket string) error {
	purl := parquetURL(bucket, col.ParquetKey)

	// Fast path: GeoParquet spec stores bbox in file metadata.
	var rawGeo interface{}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT value FROM parquet_kv_metadata('%s') WHERE key='geo'", purl,
	)).Scan(&rawGeo); err == nil {
		var b []byte
		switch v := rawGeo.(type) {
		case []byte:
			b = v
		case string:
			b = []byte(v)
		}
		var geo struct {
			Columns map[string]struct {
				Bbox []float64 `json:"bbox"`
			} `json:"columns"`
		}
		if json.Unmarshal(b, &geo) == nil {
			if bbox := geo.Columns[col.GeomColumn].Bbox; len(bbox) == 4 {
				col.Extent = [4]float64{bbox[0], bbox[1], bbox[2], bbox[3]}
				return nil
			}
		}
	}

	// Medium path: aggregate the pre-computed bbox columns using parquet column statistics.
	// DuckDB resolves MIN/MAX over simple numeric columns from parquet row-group stats
	// without reading row data, making this nearly as fast as the metadata path.
	if col.BboxColsStyle == "flat" {
		var minX, minY, maxX, maxY sql.NullFloat64
		err := s.db.QueryRowContext(ctx, fmt.Sprintf(
			"SELECT MIN(bbox_xmin), MIN(bbox_ymin), MAX(bbox_xmax), MAX(bbox_ymax) FROM read_parquet('%s')", purl,
		)).Scan(&minX, &minY, &maxX, &maxY)
		if err == nil && minX.Valid {
			col.Extent = [4]float64{minX.Float64, minY.Float64, maxX.Float64, maxY.Float64}
			return nil
		}
	}

	// Slow path: full geometry scan for files without GeoParquet metadata or bbox columns.
	g := geomExpr(col)
	query := fmt.Sprintf(`
		SELECT
			MIN(ST_XMin(ST_Envelope(%s))),
			MIN(ST_YMin(ST_Envelope(%s))),
			MAX(ST_XMax(ST_Envelope(%s))),
			MAX(ST_YMax(ST_Envelope(%s)))
		FROM read_parquet('%s')
	`, g, g, g, g, purl)

	row := s.db.QueryRowContext(ctx, query)
	var minX, minY, maxX, maxY sql.NullFloat64
	if err := row.Scan(&minX, &minY, &maxX, &maxY); err != nil {
		return err
	}
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
	purl := parquetURL(bucket, col.ParquetKey)
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

	bboxExclude := map[string]bool{
		"bbox": true, "bbox_xmin": true, "bbox_ymin": true, "bbox_xmax": true, "bbox_ymax": true,
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
		if colName == col.GeomColumn || colName == col.IDColumn || bboxExclude[strings.ToLower(colName)] {
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
			fmt.Sprintf("SELECT COUNT(*) FROM read_parquet('%s') LIMIT 1", parquetURL(bucket, col.ParquetKey)),
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

// QueryItems fetches a paginated, optionally bbox-filtered and datetime-filtered list of features.
func (s *Store) QueryItems(ctx context.Context, col *config.CollectionConfig, bucket string, limit, offset int, bbox *[4]float64, dt *DatetimeFilter) ([]Feature, int64, error) {
	purl := parquetURL(bucket, col.ParquetKey)

	g := geomExpr(col)
	where := ""
	if bbox != nil {
		minx, miny, maxx, maxy := bbox[0], bbox[1], bbox[2], bbox[3]
		// Numeric bbox predicates let DuckDB prune row groups via parquet statistics
		// before evaluating the geometry intersection (avoids unnecessary S3 range requests).
		switch col.BboxColsStyle {
		case "flat":
			where = fmt.Sprintf(
				"WHERE bbox_xmin <= %f AND bbox_xmax >= %f AND bbox_ymin <= %f AND bbox_ymax >= %f AND ST_Intersects(%s, ST_MakeEnvelope(%f, %f, %f, %f))",
				maxx, minx, maxy, miny, g, minx, miny, maxx, maxy,
			)
		case "struct":
			where = fmt.Sprintf(
				"WHERE bbox.xmin <= %f AND bbox.xmax >= %f AND bbox.ymin <= %f AND bbox.ymax >= %f AND ST_Intersects(%s, ST_MakeEnvelope(%f, %f, %f, %f))",
				maxx, minx, maxy, miny, g, minx, miny, maxx, maxy,
			)
		default:
			where = fmt.Sprintf(
				"WHERE ST_Intersects(%s, ST_MakeEnvelope(%f, %f, %f, %f))",
				g, minx, miny, maxx, maxy,
			)
		}
	}

	if dt != nil && col.DatetimeColumn != "" {
		dtCol := col.DatetimeColumn
		clause := ""
		switch {
		case dt.Exact && dt.Low != nil:
			clause = fmt.Sprintf("%s = '%s'::TIMESTAMPTZ", dtCol, dt.Low.UTC().Format(time.RFC3339))
		case dt.Low != nil && dt.High != nil:
			clause = fmt.Sprintf("%s BETWEEN '%s'::TIMESTAMPTZ AND '%s'::TIMESTAMPTZ",
				dtCol, dt.Low.UTC().Format(time.RFC3339), dt.High.UTC().Format(time.RFC3339))
		case dt.Low != nil:
			clause = fmt.Sprintf("%s >= '%s'::TIMESTAMPTZ", dtCol, dt.Low.UTC().Format(time.RFC3339))
		case dt.High != nil:
			clause = fmt.Sprintf("%s <= '%s'::TIMESTAMPTZ", dtCol, dt.High.UTC().Format(time.RFC3339))
		}
		if clause != "" {
			if where == "" {
				where = "WHERE " + clause
			} else {
				where += " AND " + clause
			}
		}
	}

	var total int64
	if err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT COUNT(*) FROM read_parquet('%s') %s", purl, where),
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count: %w", err)
	}

	// Build EXCLUDE list: geom, id, and any bbox columns (internal; not user-visible).
	excludeCols := []string{col.GeomColumn, col.IDColumn}
	switch col.BboxColsStyle {
	case "flat":
		excludeCols = append(excludeCols, "bbox_xmin", "bbox_ymin", "bbox_xmax", "bbox_ymax")
	case "struct":
		excludeCols = append(excludeCols, "bbox")
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			ST_AsGeoJSON(%s)::VARCHAR AS geometry,
			* EXCLUDE (%s)
		FROM read_parquet('%s')
		%s
		LIMIT %d OFFSET %d
	`, col.IDColumn, g, strings.Join(excludeCols, ", "), purl, where, limit, offset)

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
	purl := parquetURL(bucket, col.ParquetKey)
	g := geomExpr(col)
	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			ST_AsGeoJSON(%s)::VARCHAR AS geometry,
			* EXCLUDE (%s, %s)
		FROM read_parquet('%s')
		WHERE CAST(%s AS VARCHAR) = '%s'
		LIMIT 1
	`, col.IDColumn, g, col.GeomColumn, col.IDColumn, purl, col.IDColumn, featureID)

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
