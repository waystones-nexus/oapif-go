package api

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
)

// CORSMiddleware adds CORS response headers and handles OPTIONS preflight requests.
// allowedOrigins may be ["*"] (reflect all) or a list of specific origins.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Security headers — applied to every response from this server.
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'unsafe-eval' https://unpkg.com https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://unpkg.com https://cdn.jsdelivr.net https://fonts.googleapis.com; img-src 'self' data: blob: https:; font-src 'self' data: https://unpkg.com https://cdn.jsdelivr.net https://fonts.gstatic.com; connect-src 'self' https:; worker-src 'self' blob:;")

			origin := r.Header.Get("Origin")
			var allow string
			if len(allowedOrigins) == 1 && allowedOrigins[0] == "*" {
				allow = "*"
			} else {
				for _, o := range allowedOrigins {
					if o == origin {
						allow = origin
						break
					}
				}
			}
			if allow != "" {
				w.Header().Set("Access-Control-Allow-Origin", allow)
				w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Accept, X-Api-Key")
				w.Header().Set("Access-Control-Max-Age", "86400")
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// HeadMiddleware converts HEAD requests to GET and discards the response body
// while preserving all response headers.
func HeadMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			r.Method = http.MethodGet
			next.ServeHTTP(&headWriter{ResponseWriter: w}, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type headWriter struct{ http.ResponseWriter }

func (h *headWriter) Write(b []byte) (int, error) { return len(b), nil }

// GzipMiddleware compresses response bodies ≥ 1 KB when the client sends
// Accept-Encoding: gzip. Adds Vary: Accept-Encoding to every response.
func GzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Vary must be present even on uncompressed responses so caches work correctly.
		w.Header().Add("Vary", "Accept-Encoding")

		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		gbuf := &gzipBuf{ResponseWriter: w}
		next.ServeHTTP(gbuf, r)

		code := gbuf.code
		if code == 0 {
			code = http.StatusOK
		}
		body := gbuf.buf.Bytes()

		if len(body) >= 1024 {
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Del("Content-Length")
			w.WriteHeader(code)
			gz := gzip.NewWriter(w)
			gz.Write(body) //nolint:errcheck
			gz.Close()     //nolint:errcheck
		} else {
			w.WriteHeader(code)
			w.Write(body) //nolint:errcheck
		}
	})
}

// gzipBuf captures the response body and status code so GzipMiddleware can
// decide whether to compress after the handler completes.
// Header() is inherited from the embedded ResponseWriter so the handler
// writes directly into the real header map — headers are not flushed until
// the outer middleware calls ResponseWriter.WriteHeader().
type gzipBuf struct {
	http.ResponseWriter
	buf  bytes.Buffer
	code int
}

func (g *gzipBuf) WriteHeader(code int)        { g.code = code }
func (g *gzipBuf) Write(b []byte) (int, error) { return g.buf.Write(b) }

// FParamMiddleware maps the OGC ?f= format parameter to an Accept header so
// all downstream handlers can use r.Header.Get("Accept") uniformly.
// Returns 400 for unrecognised f values.
func FParamMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("f")
		if f == "" {
			next.ServeHTTP(w, r)
			return
		}
		var mediaType string
		switch strings.ToLower(f) {
		case "json":
			mediaType = "application/json"
		case "geojson":
			mediaType = "application/geo+json"
		case "html":
			mediaType = "text/html"
		default:
			writeError(w, http.StatusBadRequest, "Invalid Parameter",
				"https://www.opengis.net/def/exceptions/ogcapi-features-1/1.0/InvalidParameterValue",
				"Invalid value for parameter 'f'. Supported values: json, geojson, html")
			return
		}
		r2 := r.Clone(r.Context())
		r2.Header.Set("Accept", mediaType)
		next.ServeHTTP(w, r2)
	})
}
