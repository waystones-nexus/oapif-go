package api

import "encoding/json"

type GeoJSONFeature struct {
	Type       string                 `json:"type"`
	ID         interface{}            `json:"id"`
	Geometry   json.RawMessage        `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
}

type FeatureCollection struct {
	Type           string           `json:"type"`
	NumberMatched  int64            `json:"numberMatched"`
	NumberReturned int              `json:"numberReturned"`
	Features       []GeoJSONFeature `json:"features"`
	Links          []Link           `json:"links"`
}

type SingleFeatureResponse struct {
	Type       string                 `json:"type"`
	ID         interface{}            `json:"id"`
	Geometry   json.RawMessage        `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
	Links      []Link                 `json:"links"`
}

type SpatialExtent struct {
	Bbox [][]float64 `json:"bbox"`
}

type Extent struct {
	Spatial *SpatialExtent `json:"spatial,omitempty"`
}

type CollectionInfo struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	Extent      *Extent  `json:"extent,omitempty"`
	Links       []Link   `json:"links"`
	CRS         []string `json:"crs,omitempty"`
}

type CollectionsResponse struct {
	Collections []CollectionInfo `json:"collections"`
	Links       []Link           `json:"links"`
}

type ConformanceResponse struct {
	ConformsTo []string `json:"conformsTo"`
}

type LandingPage struct {
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Links       []Link `json:"links"`
}

type QueryablesResponse struct {
	Schema     string                       `json:"$schema"`
	ID         string                       `json:"$id"`
	Type       string                       `json:"type"`
	Title      string                       `json:"title,omitempty"`
	Properties map[string]map[string]string `json:"properties"`
}
