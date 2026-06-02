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
	R2Key       string                    `json:"r2_key"`
	GeomColumn  string                    `json:"geom_column"`
	IDColumn    string                    `json:"id_column"`
	CRS         string                    `json:"crs"`
	Extent      [4]float64
	Queryables  map[string]QueryableField
}

type Config struct {
	Port        string
	R2Endpoint  string
	R2Host      string
	R2AccessKey string
	R2SecretKey string
	R2Bucket    string
	ServerURL   string
	ServerTitle string
	Collections []CollectionConfig
}

type jsonFileConfig struct {
	Collections []CollectionConfig `json:"collections"`
}

func Load() *Config {
	cfg := &Config{
		Port:        getEnv("CONTAINER_PORT", getEnv("PORT", "5000")),
		R2Endpoint:  os.Getenv("R2_ENDPOINT"),
		R2AccessKey: os.Getenv("R2_ACCESS_KEY_ID"),
		R2SecretKey: os.Getenv("R2_SECRET_ACCESS_KEY"),
		R2Bucket:    os.Getenv("R2_BUCKET"),
		ServerURL:   strings.TrimRight(getEnv("SERVER_URL", "http://localhost:5000"), "/"),
		ServerTitle: getEnv("SERVER_TITLE", "Waystones OGC API Features"),
	}

	if cfg.R2Endpoint != "" {
		if u, err := url.Parse(cfg.R2Endpoint); err == nil {
			cfg.R2Host = u.Host
		}
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
				R2Key:      os.Getenv("COLLECTION_R2_KEY"),
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
