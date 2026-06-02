package api

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/waystones/oapif-go/internal/config"
	"github.com/waystones/oapif-go/internal/cql2"
	"github.com/waystones/oapif-go/internal/db"
)

const (
	crs84URI    = "http://www.opengis.net/def/crs/OGC/1.3/CRS84"
	epsg4326URI = "http://www.opengis.net/def/crs/EPSG/0/4326"
)

type colMapEntry struct {
	ID    string    `json:"id"`
	Title string    `json:"title"`
	Bbox  []float64 `json:"bbox,omitempty"`
}

var reservedParams = map[string]bool{
	"limit": true, "offset": true, "bbox": true, "bbox-crs": true,
	"datetime": true, "filter": true, "filter-lang": true, "crs": true, "f": true,
}

// effectiveCRS returns the CRS URIs this collection advertises.
// Always includes CRS84 and EPSG:4326; adds collection.SupportedCRS.
func effectiveCRS(col *config.CollectionConfig) []string {
	base := []string{crs84URI, epsg4326URI}
	for _, c := range col.SupportedCRS {
		if c != crs84URI && c != epsg4326URI {
			base = append(base, c)
		}
	}
	return base
}

// crsURItoEPSG returns the EPSG code string for ST_Transform, or "" if no
// transform is needed (CRS84 / EPSG:4326 are the storage CRS).
func crsURItoEPSG(uri string) string {
	if uri == crs84URI || uri == epsg4326URI {
		return ""
	}
	// "http://www.opengis.net/def/crs/EPSG/0/3857" → "EPSG:3857"
	const prefix = "http://www.opengis.net/def/crs/EPSG/0/"
	if strings.HasPrefix(uri, prefix) {
		return "EPSG:" + uri[len(prefix):]
	}
	return uri // fallback: return as-is
}

func coerceValue(typ, s string) (interface{}, error) {
	switch typ {
	case "integer":
		return strconv.ParseInt(s, 10, 64)
	case "number":
		return strconv.ParseFloat(s, 64)
	case "boolean":
		switch strings.ToLower(s) {
		case "true", "1":
			return true, nil
		case "false", "0":
			return false, nil
		default:
			return nil, fmt.Errorf("expected true or false")
		}
	default:
		return s, nil
	}
}

type Handler struct {
	cfg         *config.Config
	store       *db.Store
	startTime   time.Time
	ttfbOnce    sync.Once
	openapiJSON []byte
	tmpls       *template.Template
}

func NewHandler(cfg *config.Config, store *db.Store, startTime time.Time) *Handler {
	spec, err := buildOpenAPI(cfg)
	if err != nil {
		panic("buildOpenAPI: " + err.Error())
	}
	funcMap := template.FuncMap{
		"crsLabel": func(uri string) string {
			parts := strings.Split(uri, "/")
			if len(parts) == 0 {
				return uri
			}
			return parts[len(parts)-1]
		},
		// skipProp returns true for internal properties that should not be shown to users.
		"skipProp": func(key string) bool {
			lower := strings.ToLower(key)
			return strings.HasPrefix(lower, "bbox_")
		},
		// brandStyle returns a CSS :root override block when a custom brand color is set.
		// Returns empty HTML when the color matches the default, so the base stylesheet handles it.
		"brandStyle": func() template.CSS {
			b := cfg.Brand
			if b.Color == "" || b.Color == config.DefaultBrandColor {
				return ""
			}
			return template.CSS(fmt.Sprintf(
				":root{--brand:%s;--brand-dark:%s;--brand-light:%s;--brand-border:%s}",
				b.Color, b.ColorDark, b.ColorLight, b.ColorBorder,
			))
		},
		// faviconURL returns a custom favicon URL when branding provides one, otherwise "".
		"faviconURL": func() string {
			return cfg.Brand.FaviconURL
		},
	}
	tmpls := template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
	return &Handler{
		cfg:         cfg,
		store:       store,
		startTime:   startTime,
		openapiJSON: spec,
		tmpls:       tmpls,
	}
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

func (h *Handler) renderHTML(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpls.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", 500)
	}
}

func (h *Handler) LandingPage(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.ServerURL
	if acceptsHTML(r) {
		h.renderHTML(w, "index.html", indexTmplData{
			Title:        h.cfg.ServerTitle,
			BaseURL:      base,
			Description:  h.cfg.ServerDescription,
			Provider:     h.cfg.ServerProvider,
			License:      h.cfg.ServerLicense,
			Keywords:     h.cfg.ServerKeywords,
			ContactEmail: h.cfg.ServerContactEmail,
			ContactName:  h.cfg.ServerContactName,
		})
		return
	}
	h.writeJSON(w, http.StatusOK, LandingPage{
		Title: h.cfg.ServerTitle,
		Links: []Link{
			selfLink(base + "/"),
			ConformanceLink(base),
			APILink(base),
			APIHTMLLink(base),
			{Href: base + "/collections", Rel: "data", Type: "application/json", Title: "Collections"},
		},
	})
}

func (h *Handler) Conformance(w http.ResponseWriter, r *http.Request) {
	conforms := []string{
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/core",
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/oas30",
		"http://www.opengis.net/spec/ogcapi-features-1/1.0/conf/geojson",
	}
	if acceptsHTML(r) {
		h.renderHTML(w, "conformance.html", conformanceTmplData{
			Title: h.cfg.ServerTitle, BaseURL: h.cfg.ServerURL, ConformsTo: conforms,
		})
		return
	}
	h.writeJSON(w, http.StatusOK, ConformanceResponse{ConformsTo: conforms})
}

func (h *Handler) Collections(w http.ResponseWriter, r *http.Request) {
	base := h.cfg.ServerURL
	infos := make([]CollectionInfo, 0, len(h.cfg.Collections))
	for i := range h.cfg.Collections {
		infos = append(infos, buildCollectionInfo(&h.cfg.Collections[i], base))
	}
	if acceptsHTML(r) {
		var bboxJS template.JS
		merged := [4]float64{180, 90, -180, -90}
		hasExtent := false
		for _, col := range h.cfg.Collections {
			if col.Extent != ([4]float64{}) {
				if col.Extent[0] < merged[0] {
					merged[0] = col.Extent[0]
				}
				if col.Extent[1] < merged[1] {
					merged[1] = col.Extent[1]
				}
				if col.Extent[2] > merged[2] {
					merged[2] = col.Extent[2]
				}
				if col.Extent[3] > merged[3] {
					merged[3] = col.Extent[3]
				}
				hasExtent = true
			}
		}
		if hasExtent {
			bboxJS = template.JS(fmt.Sprintf("[%f,%f,%f,%f]", merged[0], merged[1], merged[2], merged[3]))
		}
		var collectionsDataJS template.JS
		var colEntries []colMapEntry
		for _, info := range infos {
			e := colMapEntry{ID: info.ID, Title: info.Title}
			if info.Extent != nil && info.Extent.Spatial != nil && len(info.Extent.Spatial.Bbox) > 0 && len(info.Extent.Spatial.Bbox[0]) == 4 {
				b := info.Extent.Spatial.Bbox[0]
				if b[0] != 0 || b[1] != 0 || b[2] != 0 || b[3] != 0 {
					e.Bbox = b
				}
			}
			colEntries = append(colEntries, e)
		}
		if len(colEntries) > 0 {
			if d, err := json.Marshal(colEntries); err == nil {
				collectionsDataJS = template.JS(d)
			}
		}
		h.renderHTML(w, "collections.html", collectionsTmplData{
			Title: h.cfg.ServerTitle, BaseURL: base, Collections: infos,
			BboxJS: bboxJS, CollectionsDataJS: collectionsDataJS,
		})
		return
	}
	h.writeJSON(w, http.StatusOK, CollectionsResponse{
		Collections: infos,
		Links:       []Link{selfLink(base + "/collections")},
	})
}

func (h *Handler) Collection(w http.ResponseWriter, r *http.Request) {
	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		NotFound(w, fmt.Sprintf("Collection '%s' does not exist", r.PathValue("collectionId")))
		return
	}
	info := buildCollectionInfo(col, h.cfg.ServerURL)
	if acceptsHTML(r) {
		var bboxJS template.JS
		if col.Extent[0] != 0 || col.Extent[1] != 0 || col.Extent[2] != 0 || col.Extent[3] != 0 {
			bboxJS = template.JS(fmt.Sprintf("[%f,%f,%f,%f]", col.Extent[0], col.Extent[1], col.Extent[2], col.Extent[3]))
		}
		h.renderHTML(w, "collection.html", collectionTmplData{
			Title: h.cfg.ServerTitle, BaseURL: h.cfg.ServerURL, Col: info, BboxJS: bboxJS,
		})
		return
	}
	h.writeJSON(w, http.StatusOK, info)
}

func (h *Handler) Queryables(w http.ResponseWriter, r *http.Request) {
	col := h.cfg.CollectionByID(r.PathValue("collectionId"))
	if col == nil {
		NotFound(w, fmt.Sprintf("Collection '%s' does not exist", r.PathValue("collectionId")))
		return
	}

	props := make(map[string]QueryablePropertySchema, len(col.Queryables))
	for name, field := range col.Queryables {
		props[name] = QueryablePropertySchema{
			Type:        field.Type,
			Format:      field.Format,
			Title:       field.Title,
			Description: field.Description,
			Enum:        field.Enum,
		}
	}

	base := h.cfg.ServerURL
	if acceptsHTML(r) {
		h.renderHTML(w, "queryables.html", queryablesTmplData{
			Title: h.cfg.ServerTitle, BaseURL: base,
			ColID: col.ID, ColTitle: col.Title, Properties: props,
		})
		return
	}
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
		NotFound(w, fmt.Sprintf("Collection '%s' does not exist", r.PathValue("collectionId")))
		return
	}

	q := r.URL.Query()
	limit := clampInt(parseIntParam(q.Get("limit"), 10), 1, 1000)
	offset := clampInt(parseIntParam(q.Get("offset"), 0), 0, -1)

	var bbox *[4]float64
	if bboxStr := q.Get("bbox"); bboxStr != "" {
		if b, ok := parseBbox(bboxStr); ok {
			bbox = &b
		} else {
			InvalidParameter(w, "bbox", "must be minx,miny,maxx,maxy")
			return
		}
	}

	dt, err := parseDatetime(q.Get("datetime"))
	if err != nil {
		InvalidParameter(w, "datetime", err.Error())
		return
	}
	if dt != nil && col.DatetimeColumn == "" {
		InvalidParameter(w, "datetime", "collection has no datetime column")
		return
	}

	// Property equality filters
	var propSQL []string
	var propArgs []interface{}
	for k, vs := range q {
		if reservedParams[k] {
			continue
		}
		field, ok := col.Queryables[k]
		if !ok {
			InvalidParameter(w, k, "unknown property")
			return
		}
		v, err := coerceValue(field.Type, vs[0])
		if err != nil {
			InvalidParameter(w, k, err.Error())
			return
		}
		propSQL = append(propSQL, fmt.Sprintf(`"%s" = ?`, k))
		propArgs = append(propArgs, v)
	}

	// CQL2-Text filter
	if raw := q.Get("filter"); raw != "" {
		if q.Get("filter-lang") == "cql2-json" {
			writeError(w, 501, "Not Implemented",
				"https://www.rfc-editor.org/rfc/rfc9110#section-15.6.2",
				"filter-lang=cql2-json is not supported")
			return
		}
		ast, err := cql2.Parse(raw)
		if err != nil {
			InvalidParameter(w, "filter", err.Error())
			return
		}
		fsql, fargs, err := cql2.Translate(ast, col.Queryables)
		if err != nil {
			InvalidParameter(w, "filter", err.Error())
			return
		}
		propSQL = append(propSQL, "("+fsql+")")
		propArgs = append(propArgs, fargs...)
	}
	var filter *db.FilterExpr
	if len(propSQL) > 0 {
		filter = &db.FilterExpr{SQL: strings.Join(propSQL, " AND "), Args: propArgs}
	}

	// Output CRS
	outputCRS, activeCRSURI := "", crs84URI
	if crsURI := q.Get("crs"); crsURI != "" {
		if !slices.Contains(effectiveCRS(col), crsURI) {
			InvalidParameter(w, "crs", "unsupported CRS for this collection")
			return
		}
		outputCRS = crsURItoEPSG(crsURI)
		activeCRSURI = crsURI
	}

	// bbox-crs
	bboxCRS := ""
	if bboxCRSURI := q.Get("bbox-crs"); bboxCRSURI != "" {
		if !slices.Contains(effectiveCRS(col), bboxCRSURI) {
			InvalidParameter(w, "bbox-crs", "unsupported CRS for this collection")
			return
		}
		bboxCRS = crsURItoEPSG(bboxCRSURI)
	}

	opts := db.QueryOptions{
		Limit: limit, Offset: offset, Bbox: bbox, BboxCRS: bboxCRS,
		Datetime: dt, Filter: filter, OutputCRS: outputCRS,
	}
	features, total, err := h.store.QueryItems(r.Context(), col, h.cfg.S3Bucket, opts)
	if err != nil {
		log.Printf("items query error: %v", err)
		InternalServerError(w, "query failed")
		return
	}

	base := h.cfg.ServerURL
	path := fmt.Sprintf("/collections/%s/items", col.ID)
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
		links = append(links, NextLink(base, path, offset+limit, limit, q))
	}
	if offset > 0 {
		prev := offset - limit
		if prev < 0 {
			prev = 0
		}
		links = append(links, PrevLink(base, path, prev, limit, q))
	}

	w.Header().Set("Content-Crs", "<"+activeCRSURI+">")

	if acceptsHTML(r) {
		featJSON, _ := json.Marshal(geoFeatures)
		// Use relative paths for prev/next so they work regardless of SERVER_URL.
		linkToRelPath := func(href string) string {
			if u, err := url.Parse(href); err == nil {
				if u.RawQuery != "" {
					return u.Path + "?" + u.RawQuery
				}
				return u.Path
			}
			return href
		}
		var prevHref, nextHref string
		for _, l := range links {
			if l.Rel == "next" {
				nextHref = linkToRelPath(l.Href)
			}
			if l.Rel == "prev" {
				prevHref = linkToRelPath(l.Href)
			}
		}
		h.renderHTML(w, "items.html", itemsTmplData{
			Title:        h.cfg.ServerTitle,
			BaseURL:      base,
			ColID:        col.ID,
			Col:          buildCollectionInfo(col, base),
			FeaturesJSON: template.JS(featJSON),
			Total:        total,
			Limit:        limit,
			Offset:       offset,
			Bbox:         q.Get("bbox"),
			Datetime:     q.Get("datetime"),
			Filter:       q.Get("filter"),
			PrevHref:     prevHref,
			NextHref:     nextHref,
		})
		return
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
		NotFound(w, fmt.Sprintf("Collection '%s' does not exist", r.PathValue("collectionId")))
		return
	}
	featureID := r.PathValue("featureId")

	q := r.URL.Query()

	// Output CRS
	outputCRS, activeCRSURI := "", crs84URI
	if crsURI := q.Get("crs"); crsURI != "" {
		if !slices.Contains(effectiveCRS(col), crsURI) {
			InvalidParameter(w, "crs", "unsupported CRS for this collection")
			return
		}
		outputCRS = crsURItoEPSG(crsURI)
		activeCRSURI = crsURI
	}

	f, err := h.store.QueryItem(r.Context(), col, h.cfg.S3Bucket, featureID, outputCRS)
	if err != nil {
		log.Printf("item query error: %v", err)
		InternalServerError(w, "query failed")
		return
	}
	if f == nil {
		NotFound(w, fmt.Sprintf("Feature '%s' not found in collection '%s'", featureID, col.ID))
		return
	}

	w.Header().Set("Content-Crs", "<"+activeCRSURI+">")

	base := h.cfg.ServerURL
	resp := SingleFeatureResponse{
		Type:       "Feature",
		ID:         f.ID,
		Geometry:   f.Geometry,
		Properties: f.Properties,
		Links: []Link{
			geoSelfLink(fmt.Sprintf("%s/collections/%s/items/%s", base, col.ID, featureID)),
			collectionLink(base, col.ID),
		},
	}

	if acceptsHTML(r) {
		featJSON, _ := json.MarshalIndent(resp, "", "  ")
		h.renderHTML(w, "item.html", itemTmplData{
			Title:       h.cfg.ServerTitle,
			BaseURL:     base,
			ColID:       col.ID,
			FeatureID:   featureID,
			Feature:     resp,
			FeatureJSON: template.JS(featJSON),
		})
		return
	}

	h.writeGeoJSON(w, http.StatusOK, resp)
}

func buildCollectionInfo(col *config.CollectionConfig, base string) CollectionInfo {
	return CollectionInfo{
		ID:          col.ID,
		Title:       col.Title,
		Description: col.Description,
		Keywords:    col.Keywords,
		Extent: &Extent{
			Spatial: &SpatialExtent{
				Bbox: [][]float64{{col.Extent[0], col.Extent[1], col.Extent[2], col.Extent[3]}},
			},
		},
		Links: []Link{
			selfLink(fmt.Sprintf("%s/collections/%s", base, col.ID)),
			itemsLink(base, col.ID),
		},
		StorageCRS: crs84URI,
		CRS:        effectiveCRS(col),
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

func parseDatetime(s string) (*db.DatetimeFilter, error) {
	if s == "" {
		return nil, nil
	}
	if strings.Contains(s, "/") {
		parts := strings.SplitN(s, "/", 2)
		if parts[0] == ".." {
			t, err := time.Parse(time.RFC3339, parts[1])
			if err != nil {
				return nil, err
			}
			return &db.DatetimeFilter{High: &t}, nil
		}
		if parts[1] == ".." {
			t, err := time.Parse(time.RFC3339, parts[0])
			if err != nil {
				return nil, err
			}
			return &db.DatetimeFilter{Low: &t}, nil
		}
		t1, err := time.Parse(time.RFC3339, parts[0])
		if err != nil {
			return nil, err
		}
		t2, err := time.Parse(time.RFC3339, parts[1])
		if err != nil {
			return nil, err
		}
		return &db.DatetimeFilter{Low: &t1, High: &t2}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, err
	}
	return &db.DatetimeFilter{Low: &t, Exact: true}, nil
}
