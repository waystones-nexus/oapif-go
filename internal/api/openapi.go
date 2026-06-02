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

	doc := map[string]any{
		"openapi": "3.0.0",
		"info": map[string]any{
			"title":       cfg.ServerTitle,
			"description": "OGC API Features — served by oapif-go",
			"version":     "1.0.0",
		},
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
					"name": "bbox", "in": "query",
					"description": "Bounding box: minx,miny,maxx,maxy (CRS84)",
					"schema":      map[string]any{"type": "string"},
				},
				"datetime": map[string]any{
					"name": "datetime", "in": "query",
					"description": "RFC 3339 instant or interval (../T or T/.. or T/T)",
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
	geoResp := func(desc string) map[string]any {
		return map[string]any{
			"200": map[string]any{
				"description": desc,
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
				"responses": jsonResp("Landing page"),
			},
		},
		"/conformance": map[string]any{
			"get": map[string]any{
				"summary": "Conformance", "operationId": "getConformance",
				"responses": jsonResp("Conformance classes"),
			},
		},
		"/collections": map[string]any{
			"get": map[string]any{
				"summary": "Collections", "operationId": "getCollections",
				"responses": jsonResp("Collections"),
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
				"parameters": []any{ref("collectionId")},
				"responses":  jsonResp("Collection metadata"),
			},
		},
		"/collections/{collectionId}/queryables": map[string]any{
			"get": map[string]any{
				"summary": "Queryables", "operationId": "getQueryables",
				"parameters": []any{ref("collectionId")},
				"responses":  jsonResp("Queryable fields schema"),
			},
		},
		"/collections/{collectionId}/items": map[string]any{
			"get": map[string]any{
				"summary": "Items", "operationId": "getItems",
				"parameters": []any{ref("collectionId"), ref("limit"), ref("offset"), ref("bbox"), ref("datetime")},
				"responses":  geoResp("GeoJSON FeatureCollection"),
			},
		},
		"/collections/{collectionId}/items/{featureId}": map[string]any{
			"get": map[string]any{
				"summary": "Item", "operationId": "getItem",
				"parameters": []any{ref("collectionId"), ref("featureId")},
				"responses":  geoResp("GeoJSON Feature"),
			},
		},
	}
}

func (h *Handler) OpenAPI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.0")
	w.WriteHeader(http.StatusOK)
	w.Write(h.openapiJSON) //nolint:errcheck
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
