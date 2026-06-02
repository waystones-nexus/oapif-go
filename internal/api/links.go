package api

import (
	"fmt"
	"net/url"
	"strconv"
)

type Link struct {
	Href  string `json:"href"`
	Rel   string `json:"rel"`
	Type  string `json:"type,omitempty"`
	Title string `json:"title,omitempty"`
}

func SelfLink(href, mediaType string) Link {
	return Link{Href: href, Rel: "self", Type: mediaType}
}

func AlternateLink(href, mediaType, title string) Link {
	return Link{Href: href, Rel: "alternate", Type: mediaType, Title: title}
}

func ItemsLink(collectionURL string) Link {
	return Link{
		Href:  collectionURL + "/items",
		Rel:   "items",
		Type:  "application/geo+json",
		Title: "Items",
	}
}

func CollectionLink(collectionURL string) Link {
	return Link{Href: collectionURL, Rel: "collection", Type: "application/json"}
}

func ConformanceLink(baseURL string) Link {
	return Link{
		Href:  baseURL + "/conformance",
		Rel:   "conformance",
		Type:  "application/json",
		Title: "OGC API conformance classes",
	}
}

func APILink(baseURL string) Link {
	return Link{
		Href:  baseURL + "/api",
		Rel:   "service-desc",
		Type:  "application/vnd.oai.openapi+json;version=3.0",
		Title: "OpenAPI definition",
	}
}

func APIHTMLLink(baseURL string) Link {
	return Link{
		Href:  baseURL + "/api.html",
		Rel:   "service-doc",
		Type:  "text/html",
		Title: "API documentation",
	}
}

func NextLink(baseURL, path string, nextOffset, limit int, params url.Values) Link {
	return paginationLink("next", "Next page", baseURL, path, nextOffset, limit, params)
}

func PrevLink(baseURL, path string, prevOffset, limit int, params url.Values) Link {
	return paginationLink("prev", "Previous page", baseURL, path, prevOffset, limit, params)
}

func paginationLink(rel, title, baseURL, path string, offset, limit int, params url.Values) Link {
	q := cloneValues(params)
	q.Set("offset", strconv.Itoa(offset))
	q.Set("limit", strconv.Itoa(limit))
	return Link{
		Href:  baseURL + path + "?" + q.Encode(),
		Rel:   rel,
		Type:  "application/geo+json",
		Title: title,
	}
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		cp := make([]string, len(vs))
		copy(cp, vs)
		out[k] = cp
	}
	return out
}

// Keep unexported aliases used in handlers for backward compat during refactor.
func selfLink(href string) Link           { return SelfLink(href, "application/json") }
func geoSelfLink(href string) Link        { return SelfLink(href, "application/geo+json") }
func collectionLink(base, id string) Link { return CollectionLink(fmt.Sprintf("%s/collections/%s", base, id)) }
func itemsLink(base, id string) Link      { return ItemsLink(fmt.Sprintf("%s/collections/%s", base, id)) }
