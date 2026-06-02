package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Minimal model.json types — only the fields oapif-go needs.
// ---------------------------------------------------------------------------

type modelJSON struct {
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	CRS          string       `json:"crs"`
	SupportedCRS []string     `json:"supportedCRS"`
	Layers       []modelLayer `json:"layers"`
	Metadata     *modelMeta   `json:"metadata"`
}

type modelLayer struct {
	ID                 string         `json:"id"`
	Name               string         `json:"name"`
	Title              string         `json:"title"`
	Description        string         `json:"description"`
	GeometryColumnName string         `json:"geometryColumnName"`
	PrimaryKeyColumn   string         `json:"primaryKeyColumn"`
	IsAbstract         bool           `json:"isAbstract"`
	Extends            string         `json:"extends"`
	Properties         []modelField   `json:"properties"`
}

type modelField struct {
	Name        string            `json:"name"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	FieldType   modelFieldType    `json:"fieldType"`
	Constraints *modelConstraints `json:"constraints"`
}

type modelFieldType struct {
	Kind     string `json:"kind"`
	BaseType string `json:"baseType"` // for primitive
	Mode     string `json:"mode"`     // for codelist
	Values   []struct {
		Code string `json:"code"`
		ID   string `json:"id"`
	} `json:"values"`
}

type modelConstraints struct {
	Enumeration []string `json:"enumeration"`
}

type modelMeta struct {
	SpatialExtent *modelSpatialExtent `json:"spatialExtent"`
}

type modelSpatialExtent struct {
	WestBoundLongitude string `json:"westBoundLongitude"`
	EastBoundLongitude string `json:"eastBoundLongitude"`
	SouthBoundLatitude string `json:"southBoundLatitude"`
	NorthBoundLatitude string `json:"northBoundLatitude"`
}

// ---------------------------------------------------------------------------
// loadFromModel reads a Waystones model.json and returns CollectionConfigs.
// keyPrefix is the layerKeyPrefix from the R2Config, e.g.
// "users/abc/deployments/xyz/layers". The S3 bucket is read separately from
// S3_BUCKET env var. Returns the derived server title as the second value.
// ---------------------------------------------------------------------------
func loadFromModel(path, keyPrefix string) ([]CollectionConfig, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var m modelJSON
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, "", err
	}

	// Build per-model supported CRS list (normalized, deduplicated).
	seenCRS := map[string]bool{}
	var modelSupportedCRS []string
	addCRS := func(crs string) {
		uri := normalizeOGCCRS(crs)
		if uri != "" && !seenCRS[uri] {
			seenCRS[uri] = true
			modelSupportedCRS = append(modelSupportedCRS, uri)
		}
	}
	if m.CRS != "" && !isModelWgs84(m.CRS) {
		addCRS(m.CRS)
	}
	for _, crs := range m.SupportedCRS {
		addCRS(crs)
	}

	// Build layer lookup map for inheritance resolution.
	layerByID := make(map[string]*modelLayer, len(m.Layers))
	for i := range m.Layers {
		layerByID[m.Layers[i].ID] = &m.Layers[i]
	}

	var collections []CollectionConfig
	for i := range m.Layers {
		layer := &m.Layers[i]
		if layer.IsAbstract {
			continue
		}

		id := toTableName(layer.Name)
		parquetKey := id + ".parquet"
		if keyPrefix != "" {
			parquetKey = keyPrefix + "/" + id + ".parquet"
		}

		geomCol := layer.GeometryColumnName
		if geomCol == "" {
			geomCol = "geom"
		}
		idCol := layer.PrimaryKeyColumn
		if idCol == "" {
			idCol = "fid"
		}
		title := layer.Title
		if title == "" {
			title = layer.Name
		}

		// Resolve inherited properties and build Queryables.
		props := resolveModelProperties(layer, layerByID)
		queryables := make(map[string]QueryableField, len(props))
		for _, prop := range props {
			if prop.FieldType.Kind == "geometry" {
				continue
			}
			queryables[prop.Name] = modelFieldToQueryable(prop)
		}

		col := CollectionConfig{
			ID:           id,
			Title:        title,
			Description:  layer.Description,
			ParquetKey:   parquetKey,
			GeomColumn:   geomCol,
			IDColumn:     idCol,
			CRS:          "CRS84",
			SupportedCRS: modelSupportedCRS,
			Queryables:   queryables,
		}

		// Parse spatial extent from model metadata.
		if m.Metadata != nil && m.Metadata.SpatialExtent != nil {
			ext := m.Metadata.SpatialExtent
			w := parseFloatStr(ext.WestBoundLongitude)
			s := parseFloatStr(ext.SouthBoundLatitude)
			e := parseFloatStr(ext.EastBoundLongitude)
			n := parseFloatStr(ext.NorthBoundLatitude)
			if w != 0 || s != 0 || e != 0 || n != 0 {
				col.Extent = [4]float64{w, s, e, n}
			}
		}

		collections = append(collections, col)
	}
	return collections, m.Name, nil
}

// resolveModelProperties walks the layer inheritance chain (base-first) and
// returns the merged property list, with child properties overriding parents.
func resolveModelProperties(layer *modelLayer, byID map[string]*modelLayer) []modelField {
	var chain []*modelLayer
	visited := map[string]bool{}
	cur := layer
	for cur != nil {
		chain = append([]*modelLayer{cur}, chain...) // prepend → base layers first
		if cur.Extends == "" || visited[cur.Extends] {
			break
		}
		visited[cur.Extends] = true
		cur = byID[cur.Extends]
	}
	propMap := make(map[string]modelField)
	var order []string
	for _, l := range chain {
		for _, p := range l.Properties {
			if _, exists := propMap[p.Name]; !exists {
				order = append(order, p.Name)
			}
			propMap[p.Name] = p
		}
	}
	result := make([]modelField, 0, len(order))
	for _, name := range order {
		result = append(result, propMap[name])
	}
	return result
}

// modelFieldToQueryable converts a Waystones model Field to a QueryableField.
func modelFieldToQueryable(f modelField) QueryableField {
	qf := QueryableField{
		Title:       f.Title,
		Description: f.Description,
	}

	switch f.FieldType.Kind {
	case "primitive":
		switch strings.ToLower(f.FieldType.BaseType) {
		case "integer", "int", "int4", "int8", "integer32", "integer64",
			"bigint", "long", "short", "smallint", "tinyint":
			qf.Type = "integer"
		case "number", "float", "float4", "float8",
			"double", "decimal", "numeric", "real":
			qf.Type = "number"
		case "boolean", "bool":
			qf.Type = "boolean"
		case "date":
			qf.Type = "string"
			qf.Format = "date"
		case "date-time", "timestamp", "timestamptz":
			qf.Type = "string"
			qf.Format = "date-time"
		default:
			qf.Type = "string"
		}
	case "codelist":
		qf.Type = "string"
		if f.FieldType.Mode == "inline" {
			for _, v := range f.FieldType.Values {
				code := v.Code
				if code == "" {
					code = v.ID
				}
				if code != "" {
					qf.Enum = append(qf.Enum, code)
				}
			}
		}
	default:
		qf.Type = "string"
	}

	// Explicit constraint enumeration overrides codelist values.
	if f.Constraints != nil && len(f.Constraints.Enumeration) > 0 {
		qf.Enum = f.Constraints.Enumeration
	}

	return qf
}

// toTableName mirrors the Waystones TypeScript nameSanitizer.toTableName:
// lowercase, replace non-alphanumeric with underscores, squeeze, trim.
func toTableName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	s := b.String()
	for strings.Contains(s, "__") {
		s = strings.ReplaceAll(s, "__", "_")
	}
	return strings.Trim(s, "_")
}

func isModelWgs84(crs string) bool {
	return crs == "EPSG:4326" || crs == "4326" || crs == "CRS84" ||
		strings.Contains(crs, "CRS84") || strings.Contains(crs, "EPSG/0/4326")
}

func parseFloatStr(s string) float64 {
	f, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return f
}
