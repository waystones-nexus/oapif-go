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

// acceptsHTML returns true when the client prefers text/html and has not
// explicitly requested JSON. Browsers send text/html first; API clients
// typically send application/json or application/geo+json.
func acceptsHTML(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	if strings.Contains(accept, "application/json") ||
		strings.Contains(accept, "application/geo+json") {
		return false
	}
	return strings.Contains(accept, "text/html")
}
