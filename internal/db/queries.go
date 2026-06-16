package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/waystones/oapif-go/internal/config"
)

var epsgRe = regexp.MustCompile(`^EPSG:\d+$`)

// DatetimeFilter represents a parsed OGC API datetime parameter.
type DatetimeFilter struct {
	Low   *time.Time // nil = unbounded start; also used as exact value when Exact=true
	High  *time.Time // nil = unbounded end
	Exact bool       // if true, Low is an equality match
}

// FilterExpr holds a parameterized SQL WHERE fragment produced by property filters or CQL2.
type FilterExpr struct {
	SQL  string
	Args []interface{}
}

// SortField describes one column in an ORDER BY clause.
type SortField struct {
	Column string
	Desc   bool
}

// QueryOptions bundles all query parameters for QueryItems.
type QueryOptions struct {
	Limit      int
	Offset     int
	Bbox       *[4]float64
	BboxCRS    string          // EPSG code e.g. "EPSG:3857"; "" = CRS84 (no transform)
	Datetime   *DatetimeFilter
	Filter     *FilterExpr     // parameterized WHERE fragment; nil = no filter
	OutputCRS  string          // EPSG code for output geometry; "" = CRS84 (no transform)
	SortBy     []SortField     // nil → default ORDER BY id ASC
	Properties []string        // nil → all non-system columns
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
		Encoding      string          `json:"encoding"`
		Bbox          []float64       `json:"bbox"`
		GeometryTypes []string        `json:"geometry_types"`
		Crs           json.RawMessage `json:"crs"` // PROJJSON object or null (null = CRS84)
	}
	type geoMeta struct {
		PrimaryColumn string                `json:"primary_column"`
		Columns       map[string]geoColMeta `json:"columns"`
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

	// Detect geometry type from GeoParquet metadata.
	if col.GeometryType == "" {
		if cm, ok := geo.Columns[col.GeomColumn]; ok && len(cm.GeometryTypes) > 0 {
			col.GeometryType = simplifyGeomType(cm.GeometryTypes[0])
		}
	}

	// Detect storage CRS from GeoParquet metadata. Per the spec, absent/null → CRS84.
	if col.StorageCRS == "" {
		if cm, ok := geo.Columns[col.GeomColumn]; ok {
			col.StorageCRS = geoParquetCRSToURI(cm.Crs)
		}
	}

	return nil
}

// DetectGeomType reads only the GeoParquet `geo` footer metadata to set col.GeometryType.
// It is fast (no row scan) and is called after ApplySidecar so that stale sidecar values
// (e.g. "polygon" written when WKB geometry type detection failed) are corrected.
func (s *Store) DetectGeomType(ctx context.Context, col *config.CollectionConfig, bucket string) {
	purl := parquetURL(bucket, col.ParquetKey)
	type geoColMeta struct {
		GeometryTypes []string `json:"geometry_types"`
	}
	type geoMeta struct {
		PrimaryColumn string                `json:"primary_column"`
		Columns       map[string]geoColMeta `json:"columns"`
	}
	var rawGeo interface{}
	if err := s.db.QueryRowContext(ctx, fmt.Sprintf(
		"SELECT value FROM parquet_kv_metadata('%s') WHERE key='geo'", purl,
	)).Scan(&rawGeo); err != nil {
		return
	}
	var b []byte
	switch v := rawGeo.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	}
	var geo geoMeta
	if err := json.Unmarshal(b, &geo); err != nil {
		return
	}
	if cm, ok := geo.Columns[col.GeomColumn]; ok && len(cm.GeometryTypes) > 0 {
		col.GeometryType = simplifyGeomType(cm.GeometryTypes[0])
	}
}

// simplifyGeomType maps GeoParquet geometry type strings to one of "polygon", "line", "point".
func simplifyGeomType(t string) string {
	switch strings.ToLower(t) {
	case "point", "multipoint":
		return "point"
	case "linestring", "multilinestring":
		return "line"
	default:
		return "polygon"
	}
}

// geoParquetCRSToURI converts a GeoParquet PROJJSON `crs` field to an OGC CRS URI.
// Per the GeoParquet spec, null or absent means WGS84 geographic (CRS84).
func geoParquetCRSToURI(crsJSON json.RawMessage) string {
	const crs84 = "http://www.opengis.net/def/crs/OGC/1.3/CRS84"
	if len(crsJSON) == 0 || string(crsJSON) == "null" {
		return crs84
	}
	var proj struct {
		ID struct {
			Authority string      `json:"authority"`
			Code      interface{} `json:"code"` // spec allows int or string
		} `json:"id"`
	}
	if err := json.Unmarshal(crsJSON, &proj); err != nil || proj.ID.Authority == "" {
		return crs84
	}
	if strings.ToUpper(proj.ID.Authority) == "EPSG" {
		code := fmt.Sprintf("%v", proj.ID.Code)
		if code == "4326" {
			return crs84
		}
		return "http://www.opengis.net/def/crs/EPSG/0/" + code
	}
	return crs84
}

// geomExpr returns the SQL expression that produces a DuckDB GEOMETRY value from the geometry column.
// Native GEOMETRY columns need no wrapper; WKB BLOB columns require ST_GeomFromWKB().
func geomExpr(col *config.CollectionConfig) string {
	if col.GeomIsNative {
		return col.GeomColumn
	}
	return fmt.Sprintf("ST_GeomFromWKB(%s)", col.GeomColumn)
}

// storageCRSEPSG returns the EPSG code for the collection's storage CRS (e.g. "EPSG:3857").
// Defaults to "EPSG:4326" for CRS84 or when the storage CRS is unset.
func storageCRSEPSG(col *config.CollectionConfig) string {
	const crs84 = "http://www.opengis.net/def/crs/OGC/1.3/CRS84"
	const epsg4326 = "http://www.opengis.net/def/crs/EPSG/0/4326"
	const epsgPrefix = "http://www.opengis.net/def/crs/EPSG/0/"
	switch {
	case col.StorageCRS == "" || col.StorageCRS == crs84 || col.StorageCRS == epsg4326:
		return "EPSG:4326"
	case strings.HasPrefix(col.StorageCRS, epsgPrefix):
		return "EPSG:" + col.StorageCRS[len(epsgPrefix):]
	default:
		return "EPSG:4326"
	}
}

// geomOutputExpr returns the SQL expression that produces a GeoJSON string from the geometry column,
// optionally re-projecting from the collection's storage CRS to outputCRS (an EPSG code string like
// "EPSG:3857"; "" means WGS84/CRS84 output).
func geomOutputExpr(col *config.CollectionConfig, outputCRS string) string {
	g := geomExpr(col)
	srcEPSG := storageCRSEPSG(col)
	dstEPSG := outputCRS
	if dstEPSG == "" {
		dstEPSG = "EPSG:4326"
	}
	if srcEPSG == dstEPSG || !epsgRe.MatchString(dstEPSG) {
		return "ST_AsGeoJSON(" + g + ")::VARCHAR"
	}
	return fmt.Sprintf("ST_AsGeoJSON(ST_Transform(%s, '%s', '%s'))::VARCHAR", g, srcEPSG, dstEPSG)
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
	if col.Queryables == nil {
		col.Queryables = make(map[string]config.QueryableField)
	}
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
		// Merge: parquet schema is authoritative for type/format, but preserve
		// description/title/enum pre-loaded from a model.json if present.
		newField := duckTypeToSchema(colType)
		if existing, ok := col.Queryables[colName]; ok {
			newField.Title = existing.Title
			newField.Description = existing.Description
			newField.Enum = existing.Enum
		}
		col.Queryables[colName] = newField
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
// and parquet metadata cache before the HTTP server starts. Collections whose feature count
// was loaded from a sidecar already exercised the S3 connection and are skipped.
func (s *Store) Warmup(ctx context.Context, cols []config.CollectionConfig, bucket string) error {
	for _, col := range cols {
		if col.FeatureCount > 0 {
			continue // sidecar read already primed the S3 connection
		}
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

// QueryItems fetches a paginated, optionally filtered list of features.
func (s *Store) QueryItems(ctx context.Context, col *config.CollectionConfig, bucket string, opts QueryOptions) ([]Feature, int64, error) {
	purl := parquetURL(bucket, col.ParquetKey)

	g := geomExpr(col)
	storageEPSG := storageCRSEPSG(col)
	where := ""
	if opts.Bbox != nil {
		minx, miny, maxx, maxy := opts.Bbox[0], opts.Bbox[1], opts.Bbox[2], opts.Bbox[3]

		// Input bbox CRS: caller supplies an EPSG code, or "" meaning CRS84 (EPSG:4326).
		inputEPSG := "EPSG:4326"
		if opts.BboxCRS != "" && epsgRe.MatchString(opts.BboxCRS) {
			inputEPSG = opts.BboxCRS
		}

		// Build the envelope expression, transforming to storage CRS when needed.
		var envExpr string
		if inputEPSG == storageEPSG {
			envExpr = fmt.Sprintf("ST_MakeEnvelope(%f, %f, %f, %f)", minx, miny, maxx, maxy)
		} else {
			envExpr = fmt.Sprintf("ST_Transform(ST_MakeEnvelope(%f, %f, %f, %f), '%s', '%s')",
				minx, miny, maxx, maxy, inputEPSG, storageEPSG)
		}

		// Numeric bbox column predicates prune parquet row groups before the geometry check,
		// but only when the input bbox is already in the storage CRS so the raw coords match.
		switch {
		case col.BboxColsStyle == "flat" && inputEPSG == storageEPSG:
			where = fmt.Sprintf(
				"WHERE bbox_xmin <= %f AND bbox_xmax >= %f AND bbox_ymin <= %f AND bbox_ymax >= %f AND ST_Intersects(%s, %s)",
				maxx, minx, maxy, miny, g, envExpr,
			)
		case col.BboxColsStyle == "struct" && inputEPSG == storageEPSG:
			where = fmt.Sprintf(
				"WHERE bbox.xmin <= %f AND bbox.xmax >= %f AND bbox.ymin <= %f AND bbox.ymax >= %f AND ST_Intersects(%s, %s)",
				maxx, minx, maxy, miny, g, envExpr,
			)
		default:
			where = fmt.Sprintf("WHERE ST_Intersects(%s, %s)", g, envExpr)
		}
	}

	if opts.Datetime != nil && col.DatetimeColumn != "" {
		dt := opts.Datetime
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

	var whereArgs []interface{}
	if opts.Filter != nil && opts.Filter.SQL != "" {
		if where == "" {
			where = "WHERE " + opts.Filter.SQL
		} else {
			where += " AND " + opts.Filter.SQL
		}
		whereArgs = opts.Filter.Args
	}

	var total int64
	hasFilter := opts.Bbox != nil || opts.Datetime != nil || (opts.Filter != nil && opts.Filter.SQL != "")
	if col.FeatureCount > 0 && !hasFilter {
		total = col.FeatureCount
	} else {
		if err := s.db.QueryRowContext(ctx,
			fmt.Sprintf("SELECT COUNT(*) FROM read_parquet('%s') %s", purl, where),
			whereArgs...,
		).Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("count: %w", err)
		}
	}

	// Build EXCLUDE list: geom, id, and any bbox columns (internal; not user-visible).
	excludeCols := []string{col.GeomColumn, col.IDColumn}
	switch col.BboxColsStyle {
	case "flat":
		excludeCols = append(excludeCols, "bbox_xmin", "bbox_ymin", "bbox_xmax", "bbox_ymax")
	case "struct":
		excludeCols = append(excludeCols, "bbox")
	}

	// Property selection: explicit list or wildcard-minus-system-cols.
	var propClause string
	if len(opts.Properties) > 0 {
		quoted := make([]string, len(opts.Properties))
		for i, p := range opts.Properties {
			quoted[i] = fmt.Sprintf(`"%s"`, p)
		}
		propClause = strings.Join(quoted, ", ")
	} else {
		propClause = fmt.Sprintf("* EXCLUDE (%s)", strings.Join(excludeCols, ", "))
	}

	// ORDER BY: explicit sortby or default to id ASC for deterministic pagination.
	var orderBy string
	if len(opts.SortBy) > 0 {
		parts := make([]string, len(opts.SortBy))
		for i, sf := range opts.SortBy {
			dir := "ASC"
			if sf.Desc {
				dir = "DESC"
			}
			parts[i] = fmt.Sprintf(`"%s" %s`, sf.Column, dir)
		}
		orderBy = " ORDER BY " + strings.Join(parts, ", ")
	} else {
		orderBy = fmt.Sprintf(` ORDER BY "%s" ASC`, col.IDColumn)
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			%s AS geometry,
			%s
		FROM read_parquet('%s')
		%s%s
		LIMIT %d OFFSET %d
	`, col.IDColumn, geomOutputExpr(col, opts.OutputCRS), propClause, purl, where, orderBy, opts.Limit, opts.Offset)

	rows, err := s.db.QueryContext(ctx, query, whereArgs...)
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

// QueryAdjacentIDs returns the IDs of the features immediately before and after
// featureID in ascending ID order. Either value is empty when at the boundary.
// Only the ID column is read so this is fast even on large parquet files.
func (s *Store) QueryAdjacentIDs(ctx context.Context, col *config.CollectionConfig, bucket, featureID string) (prevID, nextID string, err error) {
	purl := parquetURL(bucket, col.ParquetKey)
	// LAG/LEAD over the ID column — DuckDB only reads that one column from parquet.
	query := fmt.Sprintf(`
		SELECT prev_id, next_id FROM (
			SELECT
				CAST(%s AS VARCHAR) AS id,
				CAST(LAG(%s) OVER (ORDER BY %s) AS VARCHAR) AS prev_id,
				CAST(LEAD(%s) OVER (ORDER BY %s) AS VARCHAR) AS next_id
			FROM read_parquet('%s')
		) WHERE id = ?
		LIMIT 1
	`, col.IDColumn, col.IDColumn, col.IDColumn, col.IDColumn, col.IDColumn, purl)
	var prev, next sql.NullString
	if err := s.db.QueryRowContext(ctx, query, featureID).Scan(&prev, &next); err != nil {
		return "", "", err
	}
	if prev.Valid {
		prevID = prev.String
	}
	if next.Valid {
		nextID = next.String
	}
	return prevID, nextID, nil
}

// QueryItem fetches a single feature by its ID column value.
// Returns (nil, nil) when the feature is not found.
// outputCRS is an EPSG code string (e.g. "EPSG:3857") for geometry reprojection; "" = no transform.
// properties is an optional list of property names to include; nil returns all non-system columns.
func (s *Store) QueryItem(ctx context.Context, col *config.CollectionConfig, bucket, featureID, outputCRS string, properties []string) (*Feature, error) {
	purl := parquetURL(bucket, col.ParquetKey)

	var propClause string
	if len(properties) > 0 {
		quoted := make([]string, len(properties))
		for i, p := range properties {
			quoted[i] = fmt.Sprintf(`"%s"`, p)
		}
		propClause = strings.Join(quoted, ", ")
	} else {
		propClause = fmt.Sprintf("* EXCLUDE (%s, %s)", col.GeomColumn, col.IDColumn)
	}

	query := fmt.Sprintf(`
		SELECT
			%s AS feature_id,
			%s AS geometry,
			%s
		FROM read_parquet('%s')
		WHERE CAST(%s AS VARCHAR) = ?
		LIMIT 1
	`, col.IDColumn, geomOutputExpr(col, outputCRS), propClause, purl, col.IDColumn)

	rows, err := s.db.QueryContext(ctx, query, featureID)
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
