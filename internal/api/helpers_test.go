package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/waystones/oapif-go/internal/config"
)

// ---------------------------------------------------------------------------
// staticETag
// ---------------------------------------------------------------------------

func TestStaticETag_Zero(t *testing.T) {
	if got := staticETag(time.Time{}); got != "" {
		t.Errorf("expected empty string for zero time, got %q", got)
	}
}

func TestStaticETag_NonZero(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	got := staticETag(ts)
	if got == "" {
		t.Error("expected non-empty ETag for non-zero time")
	}
	want := `"1700000000"`
	if got != want {
		t.Errorf("ETag: got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// checkNotModified
// ---------------------------------------------------------------------------

func TestCheckNotModified_ETagMatch(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	etag := staticETag(ts)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", etag)

	if !checkNotModified(w, r, etag, ts) {
		t.Error("expected true (304) for matching ETag")
	}
	if w.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", w.Code)
	}
}

func TestCheckNotModified_ETagWildcard(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", "*")

	if !checkNotModified(w, r, staticETag(ts), ts) {
		t.Error("expected true (304) for wildcard ETag")
	}
}

func TestCheckNotModified_IfModifiedSince_NotModified(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	future := ts.Add(time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-Modified-Since", future.UTC().Format(http.TimeFormat))

	if !checkNotModified(w, r, "", ts) {
		t.Error("expected 304 when content not modified since request time")
	}
}

func TestCheckNotModified_Modified(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	past := ts.Add(-time.Hour)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-Modified-Since", past.UTC().Format(http.TimeFormat))

	if checkNotModified(w, r, "", ts) {
		t.Error("expected false (not 304) when content is newer than request time")
	}
}

func TestCheckNotModified_NoHeaders(t *testing.T) {
	ts := time.Unix(1700000000, 0)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	if checkNotModified(w, r, staticETag(ts), ts) {
		t.Error("expected false when no conditional headers are present")
	}
}

// ---------------------------------------------------------------------------
// crsURItoEPSG
// ---------------------------------------------------------------------------

func TestCrsURItoEPSG_CRS84(t *testing.T) {
	if got := crsURItoEPSG("http://www.opengis.net/def/crs/OGC/1.3/CRS84"); got != "" {
		t.Errorf("CRS84 should return '', got %q", got)
	}
}

func TestCrsURItoEPSG_EPSG4326(t *testing.T) {
	if got := crsURItoEPSG("http://www.opengis.net/def/crs/EPSG/0/4326"); got != "" {
		t.Errorf("EPSG:4326 should return '', got %q", got)
	}
}

func TestCrsURItoEPSG_EPSG3857(t *testing.T) {
	uri := "http://www.opengis.net/def/crs/EPSG/0/3857"
	if got := crsURItoEPSG(uri); got != "EPSG:3857" {
		t.Errorf("expected 'EPSG:3857', got %q", got)
	}
}

func TestCrsURItoEPSG_Unknown(t *testing.T) {
	raw := "urn:ogc:def:crs:custom"
	if got := crsURItoEPSG(raw); got != raw {
		t.Errorf("unknown URI should pass through unchanged, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// effectiveCRS
// ---------------------------------------------------------------------------

func TestEffectiveCRS_AlwaysContainsCRS84AndEPSG4326(t *testing.T) {
	col := &config.CollectionConfig{}
	crs := effectiveCRS(col)
	has := func(uri string) bool {
		for _, c := range crs {
			if c == uri {
				return true
			}
		}
		return false
	}
	if !has("http://www.opengis.net/def/crs/OGC/1.3/CRS84") {
		t.Error("CRS84 missing from effectiveCRS")
	}
	if !has("http://www.opengis.net/def/crs/EPSG/0/4326") {
		t.Error("EPSG:4326 missing from effectiveCRS")
	}
}

func TestEffectiveCRS_AppendsSupportedCRS(t *testing.T) {
	col := &config.CollectionConfig{
		SupportedCRS: []string{"http://www.opengis.net/def/crs/EPSG/0/3857"},
	}
	crs := effectiveCRS(col)
	for _, c := range crs {
		if c == "http://www.opengis.net/def/crs/EPSG/0/3857" {
			return
		}
	}
	t.Error("EPSG:3857 missing from effectiveCRS even though in SupportedCRS")
}

func TestEffectiveCRS_DeduplicatesCRS84(t *testing.T) {
	col := &config.CollectionConfig{
		SupportedCRS: []string{"http://www.opengis.net/def/crs/OGC/1.3/CRS84"},
	}
	crs := effectiveCRS(col)
	count := 0
	for _, c := range crs {
		if c == "http://www.opengis.net/def/crs/OGC/1.3/CRS84" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("CRS84 should appear exactly once, got %d", count)
	}
}

// ---------------------------------------------------------------------------
// parseSortBy
// ---------------------------------------------------------------------------

var testQueryables = map[string]config.QueryableField{
	"name":  {Type: "string"},
	"value": {Type: "integer"},
}

func TestParseSortBy_AscDesc(t *testing.T) {
	fields, err := parseSortBy([]string{"+name,-value"}, testQueryables, "geometry")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 {
		t.Fatalf("expected 2 fields, got %d", len(fields))
	}
	if fields[0].Column != "name" || fields[0].Desc {
		t.Errorf("first field: got %+v", fields[0])
	}
	if fields[1].Column != "value" || !fields[1].Desc {
		t.Errorf("second field: got %+v", fields[1])
	}
}

func TestParseSortBy_UnknownProperty(t *testing.T) {
	_, err := parseSortBy([]string{"unknown"}, testQueryables, "geometry")
	if err == nil {
		t.Error("expected error for unknown property")
	}
}

func TestParseSortBy_GeomColumn(t *testing.T) {
	_, err := parseSortBy([]string{"geometry"}, testQueryables, "geometry")
	if err == nil {
		t.Error("expected error for geometry column")
	}
}

func TestParseSortBy_Empty(t *testing.T) {
	fields, err := parseSortBy(nil, testQueryables, "geometry")
	if err != nil {
		t.Fatal(err)
	}
	if len(fields) != 0 {
		t.Errorf("expected 0 fields for nil input, got %d", len(fields))
	}
}

// ---------------------------------------------------------------------------
// parseProperties
// ---------------------------------------------------------------------------

func TestParseProperties_Valid(t *testing.T) {
	props, err := parseProperties("name,value", testQueryables)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 2 {
		t.Errorf("expected 2 properties, got %d", len(props))
	}
}

func TestParseProperties_Unknown(t *testing.T) {
	_, err := parseProperties("unknown", testQueryables)
	if err == nil {
		t.Error("expected error for unknown property")
	}
}

func TestParseProperties_Empty(t *testing.T) {
	props, err := parseProperties("", testQueryables)
	if err != nil || props != nil {
		t.Errorf("expected nil, nil for empty input, got %v, %v", props, err)
	}
}

// ---------------------------------------------------------------------------
// parseBbox
// ---------------------------------------------------------------------------

func TestParseBbox_Valid4(t *testing.T) {
	b, ok := parseBbox("-10,-20,10,20")
	if !ok {
		t.Fatal("expected parseBbox to succeed")
	}
	if b[0] != -10 || b[1] != -20 || b[2] != 10 || b[3] != 20 {
		t.Errorf("unexpected bbox: %v", b)
	}
}

func TestParseBbox_Invalid(t *testing.T) {
	_, ok := parseBbox("not,a,valid,bbox")
	if ok {
		t.Error("expected parseBbox to fail on non-numeric input")
	}
}

func TestParseBbox_TooFewComponents(t *testing.T) {
	_, ok := parseBbox("1,2,3")
	if ok {
		t.Error("expected parseBbox to fail with fewer than 4 components")
	}
}

// ---------------------------------------------------------------------------
// parseDatetime
// ---------------------------------------------------------------------------

func TestParseDatetime_Instant(t *testing.T) {
	f, err := parseDatetime("2026-01-15T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Exact || f.Low == nil {
		t.Error("expected Exact=true and Low set for instant")
	}
}

func TestParseDatetime_ClosedInterval(t *testing.T) {
	f, err := parseDatetime("2026-01-01T00:00:00Z/2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if f.Exact {
		t.Error("expected Exact=false for interval")
	}
	if f.Low == nil || f.High == nil {
		t.Error("expected both Low and High to be set for closed interval")
	}
}

func TestParseDatetime_OpenStart(t *testing.T) {
	f, err := parseDatetime("../2026-06-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if f.Low != nil {
		t.Error("expected Low=nil for open-start interval")
	}
	if f.High == nil {
		t.Error("expected High to be set for open-start interval")
	}
}

func TestParseDatetime_OpenEnd(t *testing.T) {
	f, err := parseDatetime("2026-01-01T00:00:00Z/..")
	if err != nil {
		t.Fatal(err)
	}
	if f.Low == nil {
		t.Error("expected Low to be set for open-end interval")
	}
	if f.High != nil {
		t.Error("expected High=nil for open-end interval")
	}
}

func TestParseDatetime_Empty(t *testing.T) {
	f, err := parseDatetime("")
	if err != nil || f != nil {
		t.Errorf("expected nil, nil for empty input, got %v, %v", f, err)
	}
}

func TestParseDatetime_Invalid(t *testing.T) {
	_, err := parseDatetime("not-a-date")
	if err == nil {
		t.Error("expected error for invalid datetime")
	}
}

// ---------------------------------------------------------------------------
// clampInt / parseIntParam
// ---------------------------------------------------------------------------

func TestClampInt(t *testing.T) {
	tests := []struct{ v, min, max, want int }{
		{5, 1, 10, 5},
		{0, 1, 10, 1},
		{15, 1, 10, 10},
		{5, 1, -1, 5}, // max=-1 means no upper bound
	}
	for _, tt := range tests {
		got := clampInt(tt.v, tt.min, tt.max)
		if got != tt.want {
			t.Errorf("clampInt(%d,%d,%d) = %d, want %d", tt.v, tt.min, tt.max, got, tt.want)
		}
	}
}

func TestParseIntParam(t *testing.T) {
	if got := parseIntParam("42", 10); got != 42 {
		t.Errorf("expected 42, got %d", got)
	}
	if got := parseIntParam("", 10); got != 10 {
		t.Errorf("expected default 10, got %d", got)
	}
	if got := parseIntParam("abc", 10); got != 10 {
		t.Errorf("expected default 10 for invalid input, got %d", got)
	}
}
