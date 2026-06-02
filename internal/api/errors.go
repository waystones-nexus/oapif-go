package api

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type ProblemDetail struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func writeError(w http.ResponseWriter, status int, title, typeURI, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(ProblemDetail{ //nolint:errcheck
		Type:   typeURI,
		Title:  title,
		Status: status,
		Detail: detail,
	})
}

func NotFound(w http.ResponseWriter, detail string) {
	writeError(w, 404, "Not Found",
		"https://www.rfc-editor.org/rfc/rfc9110#section-15.5.5", detail)
}

func BadRequest(w http.ResponseWriter, detail string) {
	writeError(w, 400, "Bad Request",
		"https://www.rfc-editor.org/rfc/rfc9110#section-15.5.1", detail)
}

func InternalServerError(w http.ResponseWriter, detail string) {
	writeError(w, 500, "Internal Server Error",
		"https://www.rfc-editor.org/rfc/rfc9110#section-15.6.1", detail)
}

func InvalidParameter(w http.ResponseWriter, param, detail string) {
	writeError(w, 400, "Invalid Parameter",
		"https://www.opengis.net/def/exceptions/ogcapi-features-1/1.0/InvalidParameterValue",
		fmt.Sprintf("Invalid value for parameter '%s': %s", param, detail))
}
