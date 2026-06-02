package api

import "fmt"

func selfLink(href string) Link {
	return Link{Href: href, Rel: "self", Type: "application/json"}
}

func geoSelfLink(href string) Link {
	return Link{Href: href, Rel: "self", Type: "application/geo+json"}
}

func collectionLink(baseURL, collectionID string) Link {
	return Link{
		Href: fmt.Sprintf("%s/collections/%s", baseURL, collectionID),
		Rel:  "collection",
		Type: "application/json",
	}
}

func itemsLink(baseURL, collectionID string) Link {
	return Link{
		Href:  fmt.Sprintf("%s/collections/%s/items", baseURL, collectionID),
		Rel:   "items",
		Type:  "application/geo+json",
		Title: "Items",
	}
}

func nextPageLink(baseURL, collectionID string, limit, nextOffset int) Link {
	return Link{
		Href:  fmt.Sprintf("%s/collections/%s/items?limit=%d&offset=%d", baseURL, collectionID, limit, nextOffset),
		Rel:   "next",
		Type:  "application/geo+json",
		Title: "Next page",
	}
}
