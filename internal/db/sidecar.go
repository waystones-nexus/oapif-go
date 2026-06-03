package db

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/waystones/oapif-go/internal/config"
)

// TryLoadSidecar fetches {parquetKey}.meta.json from S3 using the AWS SDK.
//
//   - (sidecar, nil) — file found and parsed successfully
//   - (nil, nil)     — file not found or inaccessible (normal on first deploy)
//   - (nil, err)     — file found but JSON is malformed
func TryLoadSidecar(ctx context.Context, s3c *s3.Client, bucket, parquetKey string) (*config.MetaSidecar, error) {
	resp, err := s3c.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(parquetKey + ".meta.json"),
	})
	if err != nil {
		// Not found, access denied, or any other S3 error — treat as absent.
		return nil, nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil
	}

	var sc config.MetaSidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return nil, fmt.Errorf("parse sidecar for %s: %w", parquetKey, err)
	}
	return &sc, nil
}

// ApplySidecar populates col from a MetaSidecar, preserving any title/description/enum
// values already loaded from a model.json (e.g. from COLLECTION_CONFIG_B64).
func ApplySidecar(col *config.CollectionConfig, sc *config.MetaSidecar) {
	col.Extent = [4]float64{sc.Extent.MinX, sc.Extent.MinY, sc.Extent.MaxX, sc.Extent.MaxY}
	col.FeatureCount = sc.FeatureCount
	col.GeneratedAt = sc.GeneratedAt
	col.GeomColumn = sc.GeomColumn
	col.IDColumn = sc.IDColumn
	col.GeomIsNative = sc.GeomIsNative
	col.BboxColsStyle = sc.BboxColsStyle
	if sc.GeometryType != "" {
		col.GeometryType = sc.GeometryType
	}
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
