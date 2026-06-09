package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/waystones/oapif-go/internal/config"
)

// buildOpenAPI constructs and marshals an OpenAPI 3.0 document for the server.
func buildOpenAPI(cfg *config.Config) ([]byte, error) {
	collectionIDs := make([]string, len(cfg.Collections))
	for i, c := range cfg.Collections {
		collectionIDs[i] = c.ID
	}

	conformsTo := []string{
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/core",
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/oas30",
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/geojson",
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/html",
		"http://www.opengis.net/spec/ogcapi-features-2/1.0/conf/crs",
		"http://www.opengis.net/spec/ogcapi-features-3/1.0/conf/filter",
		"http://www.opengis.net/spec/ogcapi-features-3/1.0/conf/features-filter",
		"http://www.opengis.net/spec/cql2/1.0/conf/basic-cql2",
		"http://www.opengis.net/spec/cql2/1.0/conf/advanced-comparison-operators",
		"http://www.opengis.net/spec/cql2/1.0/conf/basic-spatial-operators",
		"http://www.opengis.net/spec/cql2/1.0/conf/temporal-operators",
		"http://www.opengis.net/spec/cql2/1.0/conf/cql2-text",
		"http://www.opengis.net/spec/cql2/1.0/conf/cql2-json",
	}

	infoDesc := "OGC API Features server.\n\n" +
		"**Conformance:** Part 1 (core, OAS30, GeoJSON, HTML) · Part 2 (CRS by Reference) · " +
		"Part 3 (CQL2 filtering — text and JSON encoding, comparison, basic spatial operators, and temporal predicates).\n\n" +
		"**Extensions:** sortby for result ordering; properties for column selection."

	var contactBlock map[string]any
	if cfg.ServerContactEmail != "" || cfg.ServerContactName != "" {
		contactBlock = map[string]any{}
		if cfg.ServerContactName != "" {
			contactBlock["name"] = cfg.ServerContactName
		}
		if cfg.ServerContactEmail != "" {
			contactBlock["email"] = cfg.ServerContactEmail
		}
	}
	infoBlock := map[string]any{
		"title":       cfg.ServerTitle,
		"description": infoDesc,
		"version":     "1.0.0",
	}
	if cfg.ServerLicense != "" {
		infoBlock["license"] = map[string]any{"name": cfg.ServerLicense}
	}
	if contactBlock != nil {
		infoBlock["contact"] = contactBlock
	}

	doc := map[string]any{
		"openapi":      "3.0.0",
		"info":         infoBlock,
		"x-conformsTo": conformsTo,
		"servers": []map[string]any{
			{"url": cfg.ServerURL},
		},
		"paths": buildPaths(),
		"components": map[string]any{
			"parameters": map[string]any{
				"collectionId": map[string]any{
					"name":        "collectionId",
					"in":          "path",
					"required":    true,
					"description": "Collection identifier",
					"schema":      map[string]any{"type": "string", "enum": collectionIDs},
				},
				"featureId": map[string]any{
					"name":     "featureId",
					"in":       "path",
					"required": true,
					"schema":   map[string]any{"type": "string"},
				},
				"limit": map[string]any{
					"name": "limit", "in": "query",
					"description": "Maximum number of features to return",
					"schema":      map[string]any{"type": "integer", "default": 10, "minimum": 1, "maximum": 1000},
				},
				"offset": map[string]any{
					"name": "offset", "in": "query",
					"description": "Index of the first feature to return",
					"schema":      map[string]any{"type": "integer", "default": 0, "minimum": 0},
				},
				"bbox": map[string]any{
					"name":        "bbox",
					"in":          "query",
					"description": "Bounding box filter: minx,miny,maxx,maxy in CRS84",
					"explode":     false,
					"style":       "form",
					"schema": map[string]any{
						"type":     "array",
						"minItems": 4,
						"maxItems": 6,
						"items":    map[string]any{"type": "number"},
					},
				},
				"datetime": map[string]any{
					"name": "datetime", "in": "query",
					"description": "RFC 3339 instant or interval (../T or T/.. or T/T)",
					"schema":      map[string]any{"type": "string"},
				},
				"filter": map[string]any{
					"name":        "filter",
					"in":          "query",
					"description": "CQL2 filter expression. Supports comparison (=, <>, <, >, <=, >=), logical (AND, OR, NOT), LIKE, BETWEEN, IN, IS NULL, spatial predicates (S_INTERSECTS, S_WITHIN, S_CONTAINS, S_DISJOINT, S_OVERLAPS, S_TOUCHES, S_CROSSES, S_EQUALS) with WKT or BBOX geometry literals, and temporal predicates (T_AFTER, T_BEFORE, T_DURING, T_EQUALS).",
					"schema":      map[string]any{"type": "string"},
				},
				"filter-lang": map[string]any{
					"name":        "filter-lang",
					"in":          "query",
					"description": "Filter encoding language. `cql2-text` (default) or `cql2-json`.",
					"schema":      map[string]any{"type": "string", "enum": []string{"cql2-text", "cql2-json"}, "default": "cql2-text"},
				},
				"f": map[string]any{
					"name":        "f",
					"in":          "query",
					"description": "Response format. Overrides the `Accept` header. `json` and `geojson` return structured data; `html` returns a human-readable page.",
					"schema":      map[string]any{"type": "string", "enum": []string{"json", "geojson", "html"}},
				},
				"sortby": map[string]any{
					"name":        "sortby",
					"in":          "query",
					"description": "Sort order for returned features. Comma-separated property names; prefix with `-` for descending or `+`/no prefix for ascending. Example: `sortby=name,-area`.",
					"schema":      map[string]any{"type": "string"},
				},
				"properties": map[string]any{
					"name":        "properties",
					"in":          "query",
					"description": "Comma-separated list of property names to include in the response. When omitted, all properties are returned. The feature ID and geometry are always included.",
					"schema":      map[string]any{"type": "string"},
				},
				"crs": map[string]any{
					"name":        "crs",
					"in":          "query",
					"description": "OGC URI of the output coordinate reference system for geometry coordinates. Defaults to CRS84 (`http://www.opengis.net/def/crs/OGC/1.3/CRS84`). Only CRS values listed in the collection's `crs` array are accepted.",
					"schema":      map[string]any{"type": "string"},
				},
				"bbox-crs": map[string]any{
					"name":        "bbox-crs",
					"in":          "query",
					"description": "OGC URI of the CRS used by the `bbox` parameter. Defaults to CRS84. Only values from the collection's `crs` array are accepted.",
					"schema":      map[string]any{"type": "string"},
				},
			},
			"schemas": map[string]any{
				"FeatureCollection": map[string]any{
					"type":     "object",
					"required": []string{"type", "features"},
					"properties": map[string]any{
						"type":           map[string]any{"type": "string", "enum": []string{"FeatureCollection"}},
						"features":       map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Feature"}},
						"numberMatched":  map[string]any{"type": "integer"},
						"numberReturned": map[string]any{"type": "integer"},
						"links":          map[string]any{"type": "array"},
					},
				},
				"Feature": map[string]any{
					"type":     "object",
					"required": []string{"type", "geometry", "properties"},
					"properties": map[string]any{
						"type":       map[string]any{"type": "string", "enum": []string{"Feature"}},
						"id":         map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "integer"}}},
						"geometry":   map[string]any{"type": "object", "nullable": true},
						"properties": map[string]any{"type": "object", "nullable": true},
					},
				},
			},
		},
	}

	return json.MarshalIndent(doc, "", "  ")
}

func buildPaths() map[string]any {
	ref := func(name string) map[string]any {
		return map[string]any{"$ref": "#/components/parameters/" + name}
	}
	jsonResp := func(desc string) map[string]any {
		return map[string]any{
			"200": map[string]any{
				"description": desc,
				"content":     map[string]any{"application/json": map[string]any{}},
			},
		}
	}
	contentCrsHeader := map[string]any{
		"Content-Crs": map[string]any{
			"description": "OGC URI of the CRS used for geometry coordinates in the response, enclosed in angle brackets.",
			"schema":      map[string]any{"type": "string"},
		},
	}
	geoResp := func(desc string) map[string]any {
		return map[string]any{
			"200": map[string]any{
				"description": desc,
				"headers":     contentCrsHeader,
				"content": map[string]any{
					"application/geo+json": map[string]any{
						"schema": map[string]any{"$ref": "#/components/schemas/FeatureCollection"},
					},
				},
			},
		}
	}

	return map[string]any{
		"/": map[string]any{
			"get": map[string]any{
				"summary": "Landing page", "operationId": "getLandingPage",
				"parameters": []any{ref("f")},
				"responses":  jsonResp("Landing page"),
			},
		},
		"/conformance": map[string]any{
			"get": map[string]any{
				"summary": "Conformance", "operationId": "getConformance",
				"parameters": []any{ref("f")},
				"responses":  jsonResp("Conformance classes"),
			},
		},
		"/collections": map[string]any{
			"get": map[string]any{
				"summary": "Collections", "operationId": "getCollections",
				"parameters": []any{ref("f")},
				"responses":  jsonResp("Collections"),
			},
		},
		"/api": map[string]any{
			"get": map[string]any{
				"summary": "OpenAPI definition", "operationId": "getAPI",
				"responses": map[string]any{
					"200": map[string]any{
						"description": "OpenAPI 3.0 document",
						"content":     map[string]any{"application/vnd.oai.openapi+json;version=3.0": map[string]any{}},
					},
				},
			},
		},
		"/collections/{collectionId}": map[string]any{
			"get": map[string]any{
				"summary": "Collection", "operationId": "getCollection",
				"parameters": []any{ref("collectionId"), ref("f")},
				"responses":  jsonResp("Collection metadata"),
			},
		},
		"/queryables": map[string]any{
			"get": map[string]any{
				"summary": "Global queryables", "operationId": "getGlobalQueryables",
				"parameters": []any{ref("f")},
				"responses":  jsonResp("Queryable fields across all collections"),
			},
		},
		"/collections/{collectionId}/queryables": map[string]any{
			"get": map[string]any{
				"summary": "Queryables", "operationId": "getQueryables",
				"parameters": []any{ref("collectionId"), ref("f")},
				"responses":  jsonResp("Queryable fields schema"),
			},
		},
		"/collections/{collectionId}/items": map[string]any{
			"get": map[string]any{
				"summary": "Items", "operationId": "getItems",
				"parameters": []any{ref("collectionId"), ref("limit"), ref("offset"), ref("bbox"), ref("datetime"), ref("filter"), ref("filter-lang"), ref("sortby"), ref("properties"), ref("crs"), ref("bbox-crs"), ref("f")},
				"responses":  geoResp("GeoJSON FeatureCollection"),
			},
		},
		"/collections/{collectionId}/items/{featureId}": map[string]any{
			"get": map[string]any{
				"summary": "Item", "operationId": "getItem",
				"parameters": []any{ref("collectionId"), ref("featureId"), ref("properties"), ref("f")},
				"responses":  geoResp("GeoJSON Feature"),
			},
		},
	}
}

func (h *Handler) openAPIBytes() []byte {
	n := len(h.cfg.Collections)

	h.openapiMu.RLock()
	if n == h.openapiNCols {
		b := h.openapiJSON
		h.openapiMu.RUnlock()
		return b
	}
	h.openapiMu.RUnlock()

	h.openapiMu.Lock()
	defer h.openapiMu.Unlock()
	// Re-check after acquiring write lock.
	if n != h.openapiNCols {
		if spec, err := buildOpenAPI(h.cfg); err == nil {
			h.openapiJSON = spec
			h.openapiNCols = n
		}
	}
	return h.openapiJSON
}

func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	t := h.latestGeneratedAt()
	setStaticCacheHeaders(w, t)
	if checkNotModified(w, r, staticETag(t), t) {
		return
	}
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.0")
	w.WriteHeader(http.StatusOK)
	w.Write(h.openAPIBytes()) //nolint:errcheck
}

func (h *Handler) OpenAPIHTML(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	base := h.cfg.ServerURL
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="UTF-8">
  <title>%s - API Documentation</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
</head>
<body>
  <redoc spec-url="%s/api"></redoc>
  <script src="https://cdn.jsdelivr.net/npm/redoc/bundles/redoc.standalone.js"></script>
</body>
</html>`, h.cfg.ServerTitle, base)
}
