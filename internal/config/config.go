package config

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"strings"
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

	// S3-compatible storage. S3Endpoint is optional — omit for AWS S3,
	// set to the full URL for R2, MinIO, or any other S3-compatible store.
	S3Endpoint  string // e.g. https://<id>.r2.cloudflarestorage.com  (optional)
	S3Host      string // host extracted from S3Endpoint, used by DuckDB
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3Region    string // e.g. "us-east-1" for AWS, "auto" for R2
	S3URLStyle  string // "path" or "vhost" — defaults based on whether endpoint is set

	Collections []CollectionConfig
}

type jsonFileConfig struct {
	Title        string             `json:"title,omitempty"`
	Description  string             `json:"description,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	License      string             `json:"license,omitempty"`
	Keywords     []string           `json:"keywords,omitempty"`
	ContactEmail string             `json:"contact_email,omitempty"`
	ContactName  string             `json:"contact_name,omitempty"`
	Collections  []CollectionConfig `json:"collections"`
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
}

func Load() *Config {
	// Accept both S3_* (generic) and R2_* (legacy compat) env var names.
	cfg := &Config{
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

	// Extract hostname for DuckDB's endpoint parameter (no scheme).
	if cfg.S3Endpoint != "" {
		if u, err := url.Parse(cfg.S3Endpoint); err == nil {
			cfg.S3Host = u.Host
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
