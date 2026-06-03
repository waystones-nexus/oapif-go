package config

import "time"

// MetaSidecar is the pre-baked metadata sidecar written to R2 at pipeline time
// at {parquetKey}.meta.json. The server reads it at startup to skip the DuckDB
// extent/schema queries that normally add ~1500ms.
type MetaSidecar struct {
	Version        int                `json:"version"`
	GeneratedAt    time.Time          `json:"generated_at"`
	Extent         SidecarExtent      `json:"extent"`
	FeatureCount   int64              `json:"feature_count"`
	GeomColumn     string             `json:"geom_column"`
	IDColumn       string             `json:"id_column"`
	GeomIsNative   bool               `json:"geom_is_native"`
	BboxColsStyle  string             `json:"bbox_cols_style"`  // "flat", "struct", or ""
	GeometryType   string             `json:"geometry_type"`    // "polygon", "line", or "point"
	DatetimeColumn *string            `json:"datetime_column"`
	Queryables     []SidecarQueryable `json:"queryables"`
}

type SidecarExtent struct {
	MinX float64 `json:"minx"`
	MinY float64 `json:"miny"`
	MaxX float64 `json:"maxx"`
	MaxY float64 `json:"maxy"`
}

// SidecarQueryable uses simplified JSON Schema types: string, number, integer,
// boolean, datetime. Geometry and BLOB columns are excluded.
type SidecarQueryable struct {
	Name string `json:"name"`
	Type string `json:"type"`
}
