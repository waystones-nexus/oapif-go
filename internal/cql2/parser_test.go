package cql2_test

import (
	"strings"
	"testing"

	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/cql2"
)

// testContext returns a TranslateContext with typical collection metadata for tests.
func testContext() cql2.TranslateContext {
	return cql2.TranslateContext{
		Queryables: map[string]config.QueryableField{
			"name":       {Type: "string"},
			"population": {Type: "integer"},
			"score":      {Type: "number"},
			"active":     {Type: "boolean"},
			"created_at": {Type: "string", Format: "date-time"},
		},
		GeomColumn:     "geometry",
		GeomIsNative:   false,
		DatetimeColumn: "created_at",
	}
}

// mustParse panics if the expression cannot be parsed; used in table tests.
func mustParse(t *testing.T, input string) cql2.Expr {
	t.Helper()
	expr, err := cql2.Parse(input)
	if err != nil {
		t.Fatalf("Parse(%q): %v", input, err)
	}
	return expr
}

func translateSQL(t *testing.T, input string) (string, []interface{}) {
	t.Helper()
	expr := mustParse(t, input)
	sql, args, err := cql2.Translate(expr, testContext())
	if err != nil {
		t.Fatalf("Translate(%q): %v", input, err)
	}
	return sql, args
}

// ---------------------------------------------------------------------------
// Basic comparison (regression – existing functionality)
// ---------------------------------------------------------------------------

func TestCompare(t *testing.T) {
	sql, args := translateSQL(t, `population > 1000`)
	if sql != `"population" > ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != int64(1000) {
		t.Errorf("args = %v", args)
	}
}

// ---------------------------------------------------------------------------
// Spatial predicates – CQL2-Text
// ---------------------------------------------------------------------------

func TestSpatialIntersectsBbox(t *testing.T) {
	sql, args := translateSQL(t, `S_INTERSECTS(geometry, BBOX(10, 59, 11, 60))`)
	if !strings.Contains(sql, "ST_Intersects") {
		t.Errorf("expected ST_Intersects in %q", sql)
	}
	if !strings.Contains(sql, "ST_MakeEnvelope(10, 59, 11, 60)") {
		t.Errorf("expected ST_MakeEnvelope in %q", sql)
	}
	if !strings.Contains(sql, "ST_GeomFromWKB") {
		t.Errorf("expected ST_GeomFromWKB wrapper for WKB column in %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %v", args)
	}
}

func TestSpatialNativeGeom(t *testing.T) {
	ctx := testContext()
	ctx.GeomIsNative = true
	expr := mustParse(t, `S_INTERSECTS(geometry, BBOX(10, 59, 11, 60))`)
	sql, _, err := cql2.Translate(expr, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sql, "ST_GeomFromWKB") {
		t.Errorf("native geom should not wrap with ST_GeomFromWKB: %q", sql)
	}
}

func TestSpatialIntersectsWKTPoint(t *testing.T) {
	sql, _ := translateSQL(t, `S_INTERSECTS(geometry, POINT(10.5 59.5))`)
	if !strings.Contains(sql, "ST_GeomFromText('POINT(10.5 59.5)')") {
		t.Errorf("expected ST_GeomFromText with POINT in %q", sql)
	}
}

func TestSpatialIntersectsPolygon(t *testing.T) {
	sql, _ := translateSQL(t, `S_INTERSECTS(geometry, POLYGON((0 0, 1 0, 1 1, 0 0)))`)
	if !strings.Contains(sql, "ST_GeomFromText('POLYGON((0 0,1 0,1 1,0 0))')") {
		t.Errorf("expected polygon WKT in %q", sql)
	}
}

func TestSpatialWithin(t *testing.T) {
	sql, _ := translateSQL(t, `S_WITHIN(geometry, BBOX(-180, -90, 180, 90))`)
	if !strings.Contains(sql, "ST_Within") {
		t.Errorf("expected ST_Within in %q", sql)
	}
}

func TestSpatialContains(t *testing.T) {
	sql, _ := translateSQL(t, `S_CONTAINS(geometry, POINT(5 50))`)
	if !strings.Contains(sql, "ST_Contains") {
		t.Errorf("expected ST_Contains in %q", sql)
	}
}

func TestSpatialDisjoint(t *testing.T) {
	sql, _ := translateSQL(t, `S_DISJOINT(geometry, BBOX(0, 0, 1, 1))`)
	if !strings.Contains(sql, "ST_Disjoint") {
		t.Errorf("expected ST_Disjoint in %q", sql)
	}
}

func TestSpatialNegativeCoords(t *testing.T) {
	sql, _ := translateSQL(t, `S_INTERSECTS(geometry, BBOX(-10.5, -5.5, 10.5, 5.5))`)
	if !strings.Contains(sql, "ST_MakeEnvelope(-10.5, -5.5, 10.5, 5.5)") {
		t.Errorf("expected negative coords in %q", sql)
	}
}

func TestSpatialCombinedWithCompare(t *testing.T) {
	sql, args := translateSQL(t, `population > 5000 AND S_INTERSECTS(geometry, BBOX(10, 59, 11, 60))`)
	if !strings.Contains(sql, `"population" > ?`) {
		t.Errorf("expected population compare in %q", sql)
	}
	if !strings.Contains(sql, "ST_Intersects") {
		t.Errorf("expected ST_Intersects in %q", sql)
	}
	if len(args) != 1 || args[0] != int64(5000) {
		t.Errorf("expected 1 arg with 5000, got %v", args)
	}
}

// ---------------------------------------------------------------------------
// Temporal predicates – CQL2-Text
// ---------------------------------------------------------------------------

func TestTemporalAfter(t *testing.T) {
	sql, args := translateSQL(t, `T_AFTER(created_at, TIMESTAMP('2024-01-01T00:00:00Z'))`)
	if sql != `"created_at" > ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != "2024-01-01T00:00:00Z" {
		t.Errorf("args = %v", args)
	}
}

func TestTemporalBefore(t *testing.T) {
	sql, args := translateSQL(t, `T_BEFORE(created_at, TIMESTAMP('2024-12-31T23:59:59Z'))`)
	if sql != `"created_at" < ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 {
		t.Errorf("expected 1 arg, got %v", args)
	}
}

func TestTemporalEquals(t *testing.T) {
	sql, args := translateSQL(t, `T_EQUALS(created_at, TIMESTAMP('2024-06-01T12:00:00Z'))`)
	if sql != `"created_at" = ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 {
		t.Errorf("expected 1 arg, got %v", args)
	}
}

func TestTemporalDuring(t *testing.T) {
	sql, args := translateSQL(t, `T_DURING(created_at, INTERVAL('2024-01-01T00:00:00Z', '2024-12-31T23:59:59Z'))`)
	if !strings.Contains(sql, `"created_at" >= ?`) || !strings.Contains(sql, `"created_at" <= ?`) {
		t.Errorf("expected BETWEEN-like SQL, got %q", sql)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %v", args)
	}
}

func TestTemporalNoDatetimeColumn(t *testing.T) {
	ctx := testContext()
	ctx.DatetimeColumn = ""
	expr := mustParse(t, `T_AFTER(created_at, TIMESTAMP('2024-01-01T00:00:00Z'))`)
	_, _, err := cql2.Translate(expr, ctx)
	if err == nil {
		t.Error("expected error when DatetimeColumn is empty")
	}
}

func TestTemporalIntervalWrongOp(t *testing.T) {
	_, err := cql2.Parse(`T_AFTER(created_at, INTERVAL('2024-01-01T00:00:00Z', '2024-12-31T23:59:59Z'))`)
	if err == nil {
		t.Error("expected parse error: INTERVAL is only valid for T_DURING")
	}
}

// ---------------------------------------------------------------------------
// CQL2-JSON
// ---------------------------------------------------------------------------

func jsonTranslateSQL(t *testing.T, jsonStr string) (string, []interface{}) {
	t.Helper()
	expr, err := cql2.ParseJSON([]byte(jsonStr))
	if err != nil {
		t.Fatalf("ParseJSON(%s): %v", jsonStr, err)
	}
	sql, args, err := cql2.Translate(expr, testContext())
	if err != nil {
		t.Fatalf("Translate after ParseJSON(%s): %v", jsonStr, err)
	}
	return sql, args
}

func TestJSONSimpleCompare(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"gt","args":[{"property":"population"},10000]}`)
	if sql != `"population" > ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != int64(10000) {
		t.Errorf("args = %v", args)
	}
}

func TestJSONAnd(t *testing.T) {
	sql, _ := jsonTranslateSQL(t, `{"op":"and","args":[{"op":"gt","args":[{"property":"population"},1000]},{"op":"lt","args":[{"property":"score"},99]}]}`)
	if !strings.Contains(sql, "AND") {
		t.Errorf("expected AND in %q", sql)
	}
}

func TestJSONSpatialBbox(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"s_intersects","args":[{"property":"geometry"},{"bbox":[10.0,59.0,11.0,60.0]}]}`)
	if !strings.Contains(sql, "ST_Intersects") {
		t.Errorf("expected ST_Intersects in %q", sql)
	}
	if !strings.Contains(sql, "ST_MakeEnvelope(10, 59, 11, 60)") {
		t.Errorf("expected ST_MakeEnvelope in %q", sql)
	}
	if len(args) != 0 {
		t.Errorf("expected no args, got %v", args)
	}
}

func TestJSONSpatialGeoJSON(t *testing.T) {
	sql, _ := jsonTranslateSQL(t, `{"op":"s_intersects","args":[{"property":"geometry"},{"type":"Point","coordinates":[10.5,59.5]}]}`)
	if !strings.Contains(sql, "ST_GeomFromGeoJSON") {
		t.Errorf("expected ST_GeomFromGeoJSON in %q", sql)
	}
}

func TestJSONTemporalAfter(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"t_after","args":[{"property":"created_at"},{"timestamp":"2024-01-01T00:00:00Z"}]}`)
	if sql != `"created_at" > ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != "2024-01-01T00:00:00Z" {
		t.Errorf("args = %v", args)
	}
}

func TestJSONTemporalDuring(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"t_during","args":[{"property":"created_at"},{"interval":["2024-01-01T00:00:00Z","2024-12-31T23:59:59Z"]}]}`)
	if !strings.Contains(sql, `"created_at" >= ?`) {
		t.Errorf("expected >= in %q", sql)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %v", args)
	}
}

func TestJSONLike(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"like","args":[{"property":"name"},"Lond%"]}`)
	if sql != `"name" LIKE ?` {
		t.Errorf("sql = %q", sql)
	}
	if len(args) != 1 || args[0] != "Lond%" {
		t.Errorf("args = %v", args)
	}
}

func TestJSONIn(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"in","args":[{"property":"name"},["Oslo","Bergen","Trondheim"]]}`)
	if !strings.Contains(sql, "IN") {
		t.Errorf("expected IN in %q", sql)
	}
	if len(args) != 3 {
		t.Errorf("expected 3 args, got %v", args)
	}
}

func TestJSONBetween(t *testing.T) {
	sql, args := jsonTranslateSQL(t, `{"op":"between","args":[{"property":"population"},1000,9999]}`)
	if !strings.Contains(sql, "BETWEEN") {
		t.Errorf("expected BETWEEN in %q", sql)
	}
	if len(args) != 2 {
		t.Errorf("expected 2 args, got %v", args)
	}
}

func TestJSONIsNull(t *testing.T) {
	sql, _ := jsonTranslateSQL(t, `{"op":"isNull","args":[{"property":"name"}]}`)
	if sql != `"name" IS NULL` {
		t.Errorf("sql = %q", sql)
	}
}

func TestJSONInvalidOp(t *testing.T) {
	_, err := cql2.ParseJSON([]byte(`{"op":"unknown_op","args":[]}`))
	if err == nil {
		t.Error("expected error for unknown op")
	}
}
