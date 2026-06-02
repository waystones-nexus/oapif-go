package db

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/waystones/oapif-go/internal/config"
)

// TryLoadSidecar tries to fetch {parquetKey}.meta.json from S3 via DuckDB httpfs.
//
//   - (sidecar, nil) — file found and parsed successfully
//   - (nil, nil)     — file not found or inaccessible (normal on first deploy)
//   - (nil, err)     — file found but JSON is malformed
func (s *Store) TryLoadSidecar(ctx context.Context, parquetKey, bucket string) (*config.MetaSidecar, error) {
	sidecarURL := fmt.Sprintf("s3://%s/%s.meta.json", bucket, parquetKey)
	var content string
	if err := s.db.QueryRowContext(ctx,
		fmt.Sprintf("SELECT content FROM read_text('%s')", sidecarURL),
	).Scan(&content); err != nil {
		// File not found, permission denied, or any S3 error — treat as absent.
		return nil, nil
	}
	var sc config.MetaSidecar
	if err := json.Unmarshal([]byte(content), &sc); err != nil {
		return nil, fmt.Errorf("parse sidecar for %s: %w", parquetKey, err)
	}
	return &sc, nil
}

// ApplySidecar populates col from a MetaSidecar, preserving any title/description/enum
// values already loaded from a model.json (e.g. from COLLECTION_CONFIG_B64).
func ApplySidecar(col *config.CollectionConfig, sc *config.MetaSidecar) {
	col.Extent = [4]float64{sc.Extent.MinX, sc.Extent.MinY, sc.Extent.MaxX, sc.Extent.MaxY}
	col.FeatureCount = sc.FeatureCount
	col.GeomColumn = sc.GeomColumn
	col.IDColumn = sc.IDColumn
	col.GeomIsNative = sc.GeomIsNative
	col.BboxColsStyle = sc.BboxColsStyle
	if sc.DatetimeColumn != nil {
		col.DatetimeColumn = *sc.DatetimeColumn
	}

	bboxExclude := map[string]bool{
		"bbox": true, "bbox_xmin": true, "bbox_ymin": true,
		"bbox_xmax": true, "bbox_ymax": true,
	}
	geomLower := strings.ToLower(col.GeomColumn)
	idLower := strings.ToLower(col.IDColumn)

	if col.Queryables == nil {
		col.Queryables = make(map[string]config.QueryableField, len(sc.Queryables))
	}
	for _, sq := range sc.Queryables {
		lower := strings.ToLower(sq.Name)
		if lower == geomLower || lower == idLower || bboxExclude[lower] {
			continue
		}
		newField := sidecarTypeToSchema(sq.Type)
		if existing, ok := col.Queryables[sq.Name]; ok {
			newField.Title = existing.Title
			newField.Description = existing.Description
			newField.Enum = existing.Enum
		}
		col.Queryables[sq.Name] = newField
	}
}

func sidecarTypeToSchema(t string) config.QueryableField {
	switch t {
	case "integer":
		return config.QueryableField{Type: "integer"}
	case "number":
		return config.QueryableField{Type: "number"}
	case "boolean":
		return config.QueryableField{Type: "boolean"}
	case "datetime":
		return config.QueryableField{Type: "string", Format: "date-time"}
	default:
		return config.QueryableField{Type: "string"}
	}
}
