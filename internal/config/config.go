package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"strings"
	"time"
)

// normalizeOGCCRS converts short-form CRS identifiers to canonical OGC URI form.
func normalizeOGCCRS(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	if strings.EqualFold(s, "CRS84") || strings.EqualFold(s, "OGC:CRS84") {
		return "http://www.opengis.net/def/crs/OGC/1.3/CRS84"
	}
	if strings.EqualFold(s, "EPSG:4326") {
		return "http://www.opengis.net/def/crs/EPSG/0/4326"
	}
	// "EPSG:XXXX" → OGC URI
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "EPSG:") {
		return "http://www.opengis.net/def/crs/EPSG/0/" + s[5:]
	}
	return s
}

type QueryableField struct {
	Type        string   `json:"type"`
	Format      string   `json:"format,omitempty"`
	Title       string   `json:"title,omitempty"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type CollectionConfig struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	Keywords    []string                  `json:"keywords,omitempty"`
	ParquetKey  string                    `json:"parquet_key"`
	GeomColumn    string   `json:"geom_column"`
	IDColumn      string   `json:"id_column"`
	CRS           string   `json:"crs"`
	SupportedCRS  []string `json:"supported_crs,omitempty"`
	Extent      [4]float64
	Queryables  map[string]QueryableField
	// GeomIsNative is true when the geometry column is already a DuckDB GEOMETRY type
	// (no ST_GeomFromWKB wrapper needed). Set by DetectColumns at startup.
	GeomIsNative bool
	// BboxColsStyle is set by DetectColumns when the parquet file contains pre-computed
	// bbox columns. "flat" = bbox_xmin/ymin/xmax/ymax; "struct" = bbox.xmin etc; "" = absent.
	BboxColsStyle string
	// DatetimeColumn is set by DetectColumns; empty if no timestamp column found.
	DatetimeColumn string
	// GeometryType is the simplified geometry type: "polygon", "line", or "point".
	// Set by DetectColumns from GeoParquet metadata; empty if unknown.
	GeometryType string
	// StorageCRS is the OGC URI of the CRS in which geometry coordinates are stored.
	// Set by DetectColumns from GeoParquet metadata; defaults to CRS84 when empty.
	StorageCRS string
	// FeatureCount is the total row count loaded from the sidecar (0 = not cached).
	// Used by QueryItems to skip the COUNT(*) query on unfiltered requests.
	FeatureCount int64
	// GeneratedAt is the pipeline timestamp from the sidecar; zero if no sidecar loaded.
	// Used for ETag and Last-Modified cache headers.
	GeneratedAt time.Time
}

// BrandPalette holds the four CSS variable values derived from a single hex brand color,
// plus optional URL overrides for the favicon and logo.
type BrandPalette struct {
	Color       string // --brand
	ColorDark   string // --brand-dark
	ColorLight  string // --brand-light
	ColorBorder string // --brand-border
	FaviconURL  string // optional; overrides the default Waystones favicon
}

// DefaultBrandColor is the fallback hex color used when no branding is configured.
const DefaultBrandColor = "#4338ca"

// defaultBrand is the Waystones indigo palette used when no branding is configured.
var defaultBrand = BrandPalette{
	Color:       "#4338ca",
	ColorDark:   "#3730a3",
	ColorLight:  "#eef2ff",
	ColorBorder: "#c7d2fe",
}

type Config struct {
	Port        string
	ServerURL   string
	ServerTitle string

	// Dataset-level metadata for white-label branding (populated from
	// COLLECTION_CONFIG_B64 JSON or individual SERVER_* env vars).
	ServerDescription  string
	ServerProvider     string
	ServerLicense      string
	ServerKeywords     []string
	ServerContactEmail string
	ServerContactName  string

	// Brand palette for CSS variable injection. Always set (falls back to defaultBrand).
	Brand BrandPalette

	// S3-compatible storage. S3Endpoint is optional — omit for AWS S3,
	// set to the full URL for R2, MinIO, or any other S3-compatible store.
	S3Endpoint  string // e.g. https://<id>.r2.cloudflarestorage.com  (optional)
	S3Host      string // host extracted from S3Endpoint, used by DuckDB
	S3UseSSL    bool   // false only when S3Endpoint's scheme is literally "http"
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3Region    string // e.g. "us-east-1" for AWS, "auto" for R2
	S3URLStyle  string // "path" or "vhost" — defaults based on whether endpoint is set

	Collections []CollectionConfig

	// CORSAllowedOrigins is the list of allowed CORS origins. ["*"] means all.
	CORSAllowedOrigins []string
}

type jsonBranding struct {
	Color       string `json:"color,omitempty"`
	ColorDark   string `json:"color_dark,omitempty"`
	ColorLight  string `json:"color_light,omitempty"`
	ColorBorder string `json:"color_border,omitempty"`
	FaviconURL  string `json:"favicon_url,omitempty"`
}

type jsonFileConfig struct {
	Title        string             `json:"title,omitempty"`
	Description  string             `json:"description,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	License      string             `json:"license,omitempty"`
	Keywords     []string           `json:"keywords,omitempty"`
	ContactEmail string             `json:"contact_email,omitempty"`
	ContactName  string             `json:"contact_name,omitempty"`
	Branding     *jsonBranding      `json:"branding,omitempty"`
	Collections  []CollectionConfig `json:"collections"`
}

// deriveBrandPalette computes the four CSS brand variables from a single hex color.
// Mirrors the TypeScript deriveBrandPalette in lib/branding/colors.ts.
func deriveBrandPalette(hex string) BrandPalette {
	hex = strings.TrimSpace(hex)
	if len(hex) != 7 || hex[0] != '#' {
		return defaultBrand
	}
	parse := func(s string) float64 {
		var v uint64
		fmt.Sscanf(s, "%x", &v)
		return float64(v) / 255
	}
	r, g, b := parse(hex[1:3]), parse(hex[3:5]), parse(hex[5:7])
	mx := math.Max(r, math.Max(g, b))
	mn := math.Min(r, math.Min(g, b))
	l := (mx + mn) / 2
	var h, s float64
	if mx != mn {
		d := mx - mn
		if l > 0.5 {
			s = d / (2 - mx - mn)
		} else {
			s = d / (mx + mn)
		}
		switch mx {
		case r:
			h = (g-b)/d
			if g < b {
				h += 6
			}
			h /= 6
		case g:
			h = ((b-r)/d + 2) / 6
		case b:
			h = ((r-g)/d + 4) / 6
		}
	}
	hDeg := math.Round(h * 360)
	sPct := math.Round(s * 100)
	lPct := math.Round(l * 100)

	toHex := func(hh, ss, ll float64) string {
		hNorm := hh / 360
		sNorm := ss / 100
		lNorm := ll / 100
		hue2rgb := func(p, q, t float64) float64 {
			if t < 0 {
				t += 1
			}
			if t > 1 {
				t -= 1
			}
			if t < 1.0/6 {
				return p + (q-p)*6*t
			}
			if t < 0.5 {
				return q
			}
			if t < 2.0/3 {
				return p + (q-p)*(2.0/3-t)*6
			}
			return p
		}
		var rr, gg, bb float64
		if sNorm == 0 {
			rr, gg, bb = lNorm, lNorm, lNorm
		} else {
			var q float64
			if lNorm < 0.5 {
				q = lNorm * (1 + sNorm)
			} else {
				q = lNorm + sNorm - lNorm*sNorm
			}
			p := 2*lNorm - q
			rr = hue2rgb(p, q, hNorm+1.0/3)
			gg = hue2rgb(p, q, hNorm)
			bb = hue2rgb(p, q, hNorm-1.0/3)
		}
		ri, gi, bi := int(math.Round(rr*255)), int(math.Round(gg*255)), int(math.Round(bb*255))
		return fmt.Sprintf("#%02x%02x%02x", ri, gi, bi)
	}

	return BrandPalette{
		Color:       hex,
		ColorDark:   toHex(hDeg, sPct, math.Max(0, lPct-10)),
		ColorLight:  toHex(hDeg, math.Max(0, sPct-20), 95),
		ColorBorder: toHex(hDeg, math.Max(0, sPct-10), 80),
	}
}

// applyJSONMeta copies top-level metadata from a parsed jsonFileConfig into cfg.
// Env vars take priority: fields already set from explicit env vars are not overwritten.
func applyJSONMeta(cfg *Config, jc *jsonFileConfig) {
	if len(jc.Collections) > 0 {
		cfg.Collections = jc.Collections
	}
	if jc.Title != "" && os.Getenv("SERVER_TITLE") == "" {
		cfg.ServerTitle = jc.Title
	}
	if jc.Description != "" && cfg.ServerDescription == "" {
		cfg.ServerDescription = jc.Description
	}
	if jc.Provider != "" && cfg.ServerProvider == "" {
		cfg.ServerProvider = jc.Provider
	}
	if jc.License != "" && cfg.ServerLicense == "" {
		cfg.ServerLicense = jc.License
	}
	if len(jc.Keywords) > 0 && len(cfg.ServerKeywords) == 0 {
		cfg.ServerKeywords = jc.Keywords
	}
	if jc.ContactEmail != "" && cfg.ServerContactEmail == "" {
		cfg.ServerContactEmail = jc.ContactEmail
	}
	if jc.ContactName != "" && cfg.ServerContactName == "" {
		cfg.ServerContactName = jc.ContactName
	}
	if jc.Branding != nil && cfg.Brand == defaultBrand {
		b := jc.Branding
		p := defaultBrand
		if b.Color != "" {
			p = deriveBrandPalette(b.Color)
			// Allow the JSON to supply pre-computed variants; fall back to derived values.
			if b.ColorDark != "" {
				p.ColorDark = b.ColorDark
			}
			if b.ColorLight != "" {
				p.ColorLight = b.ColorLight
			}
			if b.ColorBorder != "" {
				p.ColorBorder = b.ColorBorder
			}
		}
		p.FaviconURL = b.FaviconURL
		cfg.Brand = p
	}
}

func Load() *Config {
	// Accept both S3_* (generic) and R2_* (legacy compat) env var names.
	cfg := &Config{
		Brand:       defaultBrand,
		Port:        getEnv("CONTAINER_PORT", getEnv("PORT", "5000")),
		ServerURL:   strings.TrimRight(getEnv("SERVER_URL", getEnv("PYGEOAPI_SERVER_URL", "http://localhost:5000")), "/"),
		ServerTitle: getEnv("SERVER_TITLE", "Waystones OGC API Features"),

		ServerDescription:  getEnv("SERVER_DESCRIPTION", ""),
		ServerProvider:     getEnv("SERVER_PROVIDER", ""),
		ServerLicense:      getEnv("SERVER_LICENSE", ""),
		ServerContactEmail: getEnv("SERVER_CONTACT_EMAIL", ""),
		ServerContactName:  getEnv("SERVER_CONTACT_NAME", ""),

		S3Endpoint:  getEnv("S3_ENDPOINT", getEnv("AWS_ENDPOINT_URL", os.Getenv("R2_ENDPOINT"))),
		S3AccessKey: getEnv("S3_ACCESS_KEY_ID", getEnv("AWS_ACCESS_KEY_ID", os.Getenv("R2_ACCESS_KEY_ID"))),
		S3SecretKey: getEnv("S3_SECRET_ACCESS_KEY", getEnv("AWS_SECRET_ACCESS_KEY", os.Getenv("R2_SECRET_ACCESS_KEY"))),
		S3Bucket:    getEnv("S3_BUCKET", os.Getenv("R2_BUCKET")),
	}
	if kw := os.Getenv("SERVER_KEYWORDS"); kw != "" {
		cfg.ServerKeywords = strings.Split(kw, ",")
	}

	// Extract hostname for DuckDB's endpoint parameter (no scheme), and derive
	// USE_SSL from that same scheme. DuckDB's S3 secret defaults USE_SSL to true
	// (HTTPS) when the parameter is omitted — every query against a plain-HTTP
	// endpoint (AWS_ENDPOINT_URL=http://minio:9000, the local MinIO instance every
	// docker-compose/Codespaces kit uses) then fails at the TLS layer, and that
	// failure surfaces through handlers.go as nothing more specific than a generic
	// "query failed" — indistinguishable from a real query bug.
	cfg.S3UseSSL = true
	if cfg.S3Endpoint != "" {
		if u, err := url.Parse(cfg.S3Endpoint); err == nil {
			cfg.S3Host = u.Host
			if u.Scheme == "http" {
				cfg.S3UseSSL = false
			}
		}
	}

	// Region: default to "auto" when using a custom endpoint (R2/MinIO),
	// "us-east-1" for standard AWS S3.
	if r := getEnv("S3_REGION", ""); r != "" {
		cfg.S3Region = r
	} else if cfg.S3Host != "" {
		cfg.S3Region = "auto"
	} else {
		cfg.S3Region = "us-east-1"
	}

	// URL style: path-style for custom endpoints (required by R2/MinIO),
	// virtual-host style for standard AWS S3.
	if s := getEnv("S3_URL_STYLE", ""); s != "" {
		cfg.S3URLStyle = s
	} else if cfg.S3Host != "" {
		cfg.S3URLStyle = "path"
	} else {
		cfg.S3URLStyle = "vhost"
	}

	configPath := getEnv("CONFIG_PATH", "./config.json")
	if data, err := os.ReadFile(configPath); err == nil {
		var jc jsonFileConfig
		if err := json.Unmarshal(data, &jc); err == nil {
			applyJSONMeta(cfg, &jc)
		}
	}

	// COLLECTION_CONFIG_B64: base64-encoded config.json injected by Cloudflare Containers.
	// Takes effect only when no collections were loaded from the config file.
	if b64 := os.Getenv("COLLECTION_CONFIG_B64"); b64 != "" && len(cfg.Collections) == 0 {
		if data, err := base64.StdEncoding.DecodeString(b64); err == nil {
			var jc jsonFileConfig
			if err := json.Unmarshal(data, &jc); err == nil {
				applyJSONMeta(cfg, &jc)
			}
		}
	}

	// MODEL_PATH: read a Waystones model.json as an alternative/supplement to config.json.
	// MODEL_LAYER_KEY_PREFIX provides the S3 path prefix for parquet keys.
	if modelPath := os.Getenv("MODEL_PATH"); modelPath != "" && len(cfg.Collections) == 0 {
		keyPrefix := os.Getenv("MODEL_LAYER_KEY_PREFIX")
		if cols, title, err := loadFromModel(modelPath, keyPrefix); err == nil {
			cfg.Collections = cols
			if cfg.ServerTitle == "Waystones OGC API Features" && title != "" {
				cfg.ServerTitle = title
			}
		}
	}

	if len(cfg.Collections) == 0 {
		if id := os.Getenv("COLLECTION_ID"); id != "" {
			cfg.Collections = append(cfg.Collections, CollectionConfig{
				ID:         id,
				Title:      getEnv("COLLECTION_TITLE", id),
				ParquetKey: getEnv("COLLECTION_PARQUET_KEY", os.Getenv("COLLECTION_R2_KEY")),
				GeomColumn: getEnv("COLLECTION_GEOM_COLUMN", "geometry"),
				IDColumn:   getEnv("COLLECTION_ID_COLUMN", "fid"),
				CRS:        "CRS84",
			})
		}
	}

	// CORS_ALLOWED_ORIGINS: comma-separated list, or "*" (default) for all origins.
	if cors := os.Getenv("CORS_ALLOWED_ORIGINS"); cors != "" {
		var origins []string
		for _, o := range strings.Split(cors, ",") {
			if s := strings.TrimSpace(o); s != "" {
				origins = append(origins, s)
			}
		}
		if len(origins) > 0 {
			cfg.CORSAllowedOrigins = origins
		}
	}
	if len(cfg.CORSAllowedOrigins) == 0 {
		cfg.CORSAllowedOrigins = []string{"*"}
	}

	// BRAND_COLOR env var overrides any color from the JSON config.
	if bc := os.Getenv("BRAND_COLOR"); bc != "" {
		cfg.Brand = deriveBrandPalette(bc)
	}

	for i := range cfg.Collections {
		c := &cfg.Collections[i]
		if c.GeomColumn == "" {
			c.GeomColumn = "geometry"
		}
		if c.IDColumn == "" {
			c.IDColumn = "fid"
		}
		if c.CRS == "" {
			c.CRS = "CRS84"
		}
		for i, crs := range c.SupportedCRS {
			c.SupportedCRS[i] = normalizeOGCCRS(crs)
		}
	}

	return cfg
}

// ApplyB64 decodes a base64-encoded config.json and merges its collections and
// metadata into cfg. Used by the lazy-init header fallback when COLLECTION_CONFIG_B64
// was too large for the Cloudflare Containers 5 KB env var limit and the proxy
// delivers the full config via X-Waystones-OapifGo-B64 instead.
func ApplyB64(cfg *Config, b64 string) error {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("base64: %w", err)
	}
	var jc jsonFileConfig
	if err := json.Unmarshal(data, &jc); err != nil {
		return fmt.Errorf("json: %w", err)
	}
	applyJSONMeta(cfg, &jc)
	for i := range cfg.Collections {
		c := &cfg.Collections[i]
		if c.GeomColumn == "" {
			c.GeomColumn = "geometry"
		}
		if c.IDColumn == "" {
			c.IDColumn = "fid"
		}
		if c.CRS == "" {
			c.CRS = "CRS84"
		}
		for j, crs := range c.SupportedCRS {
			c.SupportedCRS[j] = normalizeOGCCRS(crs)
		}
	}
	return nil
}

func (c *Config) CollectionByID(id string) *CollectionConfig {
	for i := range c.Collections {
		if c.Collections[i].ID == id {
			return &c.Collections[i]
		}
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
