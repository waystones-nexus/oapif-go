package config

import (
	"encoding/json"
	"net/url"
	"os"
	"strings"
)

type QueryableField struct {
	Type   string `json:"type"`
	Format string `json:"format,omitempty"`
}

type CollectionConfig struct {
	ID          string                    `json:"id"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	ParquetKey  string                    `json:"parquet_key"`
	GeomColumn  string                    `json:"geom_column"`
	IDColumn    string                    `json:"id_column"`
	CRS         string                    `json:"crs"`
	Extent      [4]float64
	Queryables  map[string]QueryableField
}

type Config struct {
	Port        string
	ServerURL   string
	ServerTitle string

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
	Collections []CollectionConfig `json:"collections"`
}

func Load() *Config {
	// Accept both S3_* (generic) and R2_* (legacy compat) env var names.
	cfg := &Config{
		Port:        getEnv("CONTAINER_PORT", getEnv("PORT", "5000")),
		ServerURL:   strings.TrimRight(getEnv("SERVER_URL", "http://localhost:5000"), "/"),
		ServerTitle: getEnv("SERVER_TITLE", "Waystones OGC API Features"),

		S3Endpoint:  getEnv("S3_ENDPOINT", os.Getenv("R2_ENDPOINT")),
		S3AccessKey: getEnv("S3_ACCESS_KEY_ID", os.Getenv("R2_ACCESS_KEY_ID")),
		S3SecretKey: getEnv("S3_SECRET_ACCESS_KEY", os.Getenv("R2_SECRET_ACCESS_KEY")),
		S3Bucket:    getEnv("S3_BUCKET", os.Getenv("R2_BUCKET")),
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
		if err := json.Unmarshal(data, &jc); err == nil && len(jc.Collections) > 0 {
			cfg.Collections = jc.Collections
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
