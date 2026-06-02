package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/db"
)

type Handler struct {
	cfg       *config.Config
	store     *db.Store
	startTime time.Time
	ttfbOnce  sync.Once
}

func NewHandler(cfg *config.Config, store *db.Store, startTime time.Time) *Handler {
	return &Handler{cfg: cfg, store: store, startTime: startTime}
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (h *Handler) writeGeoJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/geo+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func (h *Handler) LandingPage(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.ServerURL
	h.writeJSON(w, http.StatusOK, LandingPage{
		Title: h.cfg.ServerTitle,
		Links: []Link{
			selfLink(base + "/"),
			{Href: base + "/conformance", Rel: "conformance", Type: "application/json", Title: "OGC API conformance classes"},
			{Href: base + "/collections", Rel: "data", Type: "application/json", Title: "Collections"},
		},
	})
}

func (h *Handler) Conformance(w http.ResponseWriter, r *http.Request) {
	h.writeJSON(w, http.StatusOK, ConformanceResponse{
		ConformsTo: []string{
			"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/core",
			"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/oas30",
			"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/geojson",
		},
	})
}

func (h *Handler) Collections(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.ServerURL
	infos := make([]CollectionInfo, 0, len(h.cfg.Collections))
	for i := range h.cfg.Collections {
		infos = append(infos, buildCollectionInfo(&h.cfg.Collections[i], base))
	}
	h.writeJSON(w, http.StatusOK, CollectionsResponse{
		Collections: infos,
		Links:       []Link{selfLink(base + "/collections")},
	})
}

func (h *Handler) Collection(w http.ResponseWriter, r *http.Request) {
	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		http.Error(w, "collection not found", http.StatusNotFound)
		return
	}
	info := buildCollectionInfo(col, h.cfg.ServerURL)
	info.CRS = []string{"http://www.opengis.net/def/crs/OGC/1.3/CRS84"}
	h.writeJSON(w, http.StatusOK, info)
}

func (h *Handler) Queryables(w http.ResponseWriter, r *http.Request) {
	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		http.Error(w, "collection not found", http.StatusNotFound)
		return
	}

	props := make(map[string]map[string]string, len(col.Queryables))
	for name, field := range col.Queryables {
		m := map[string]string{"type": field.Type}
		if field.Format != "" {
			m["format"] = field.Format
		}
		props[name] = m
	}

	base := h.cfg.ServerURL
	h.writeJSON(w, http.StatusOK, QueryablesResponse{
		Schema:     "https://json-schema.org/draft/2019-09/schema",
		ID:         fmt.Sprintf("%s/collections/%s/queryables", base, col.ID),
		Type:       "object",
		Title:      col.Title,
		Properties: props,
	})
}

func (h *Handler) Items(w http.ResponseWriter, r *http.Request) {
	h.ttfbOnce.Do(func() {
		log.Printf("[ttfb] %dms - first /items request received after startup", time.Since(h.startTime).Milliseconds())
	})

	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		http.Error(w, "collection not found", http.StatusNotFound)
		return
	}

	q := r.URL.Query()
	limit := clampInt(parseIntParam(q.Get("limit"), 10), 1, 1000)
	offset := clampInt(parseIntParam(q.Get("offset"), 0), 0, -1)

	var bbox *[4]float64
	if bboxStr := q.Get("bbox"); bboxStr != "" {
		if b, ok := parseBbox(bboxStr); ok {
			bbox = &b
		}
	}

	features, total, err := h.store.QueryItems(r.Context(), col, h.cfg.R2Bucket, limit, offset, bbox)
	if err != nil {
		log.Printf("items query error: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}

	base := h.cfg.ServerURL
	geoFeatures := make([]GeoJSONFeature, len(features))
	for i, f := range features {
		geoFeatures[i] = GeoJSONFeature{
			Type:       "Feature",
			ID:         f.ID,
			Geometry:   f.Geometry,
			Properties: f.Properties,
		}
	}

	links := []Link{geoSelfLink(base + r.RequestURI)}
	if int64(offset+limit) < total {
		links = append(links, nextPageLink(base, col.ID, limit, offset+limit))
	}

	h.writeGeoJSON(w, http.StatusOK, FeatureCollection{
		Type:           "FeatureCollection",
		NumberMatched:  total,
		NumberReturned: len(geoFeatures),
		Features:       geoFeatures,
		Links:          links,
	})
}

func (h *Handler) Item(w http.ResponseWriter, r *http.Request) {
	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		http.Error(w, "collection not found", http.StatusNotFound)
		return
	}
	featureID := r.PathValue("featureId")

	f, err := h.store.QueryItem(r.Context(), col, h.cfg.R2Bucket, featureID)
	if err != nil {
		log.Printf("item query error: %v", err)
		http.Error(w, "query failed", http.StatusInternalServerError)
		return
	}
	if f == nil {
		http.Error(w, "feature not found", http.StatusNotFound)
		return
	}

	base := h.cfg.ServerURL
	h.writeGeoJSON(w, http.StatusOK, SingleFeatureResponse{
		Type:       "Feature",
		ID:         f.ID,
		Geometry:   f.Geometry,
		Properties: f.Properties,
		Links: []Link{
			geoSelfLink(fmt.Sprintf("%s/collections/%s/items/%s", base, col.ID, featureID)),
			collectionLink(base, col.ID),
		},
	})
}

func buildCollectionInfo(col *config.CollectionConfig, base string) CollectionInfo {
	return CollectionInfo{
		ID:          col.ID,
		Title:       col.Title,
		Description: col.Description,
		Extent: &Extent{
			Spatial: &SpatialExtent{
				Bbox: [][]float64{{col.Extent[0], col.Extent[1], col.Extent[2], col.Extent[3]}},
			},
		},
		Links: []Link{
			selfLink(fmt.Sprintf("%s/collections/%s", base, col.ID)),
			itemsLink(base, col.ID),
		},
	}
}

func parseIntParam(s string, def int) int {
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if max >= 0 && v > max {
		return max
	}
	return v
}

func parseBbox(s string) ([4]float64, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 4 {
		return [4]float64{}, false
	}
	var b [4]float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return [4]float64{}, false
		}
		b[i] = v
	}
	return b, true
}
