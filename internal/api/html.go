package api

import (
	"html/template"
	"net/http"
	"strings"
)

type indexTmplData struct {
	Title        string
	BaseURL      string
	Description  string
	Provider     string
	License      string
	Keywords     []string
	ContactEmail string
	ContactName  string
}

type collectionsTmplData struct {
	Title              string
	BaseURL            string
	Collections        []CollectionInfo
	BboxJS             template.JS // merged extent across all collections; empty if unknown
	CollectionsDataJS  template.JS // JSON array of {id,title,bbox} for per-collection map rectangles
}

type collectionTmplData struct {
	Title   string
	BaseURL string
	Col     CollectionInfo
	BboxJS  template.JS
}

type itemsTmplData struct {
	Title        string
	BaseURL      string
	ColID        string
	Col          CollectionInfo
	FeaturesJSON template.JS
	Total        int64
	Limit        int
	Offset       int
	Bbox         string
	Datetime     string
	Filter       string
	PrevHref     string
	NextHref     string
}

type itemTmplData struct {
	Title       string
	BaseURL     string
	ColID       string
	FeatureID   string
	Feature     SingleFeatureResponse
	FeatureJSON template.JS
}

type conformanceTmplData struct {
	Title      string
	BaseURL    string
	ConformsTo []string
}

type queryablesTmplData struct {
	Title      string
	BaseURL    string
	ColID      string
	ColTitle   string
	Properties map[string]QueryablePropertySchema
}

// acceptsHTML returns true when the client prefers text/html and has not
// explicitly requested JSON. Browsers send text/html first; API clients
// typically send application/json or application/geo+json.
// Passing ?f=json always forces JSON regardless of Accept header.
func acceptsHTML(r *http.Request) bool {
	if r.URL.Query().Get("f") == "json" {
		return false
	}
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") ||
		strings.Contains(accept, "application/geo+json") {
		return false
	}
	return strings.Contains(accept, "text/html")
}
