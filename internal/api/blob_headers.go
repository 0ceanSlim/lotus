package api

import (
	"mime"
	"strings"

	"github.com/gin-gonic/gin"
)

// activeTypes are content types a browser would run as a document with
// script: HTML, SVG, XML and script files. Blobs are served from the same
// origin as the frontend, so one of these opened directly would run with the
// site's cookies and storage. They are served as downloads instead.
var activeTypes = map[string]bool{
	"text/html":                 true,
	"application/xhtml+xml":     true,
	"image/svg+xml":             true,
	"text/xml":                  true,
	"application/xml":           true,
	"text/xsl":                  true,
	"application/xslt+xml":      true,
	"application/rdf+xml":       true,
	"application/rss+xml":       true,
	"application/atom+xml":      true,
	"application/mathml+xml":    true,
	"text/javascript":           true,
	"application/javascript":    true,
	"application/ecmascript":    true,
	"text/cache-manifest":       true,
	"multipart/x-mixed-replace": true,
}

// isActiveType reports whether a content type (with or without parameters)
// is one a browser may execute.
func isActiveType(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mt = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	}
	if activeTypes[mt] {
		return true
	}
	return strings.HasSuffix(mt, "+xml")
}

// setBlobHeaders marks a served blob as inert data: the browser must not
// guess another type, the document gets a sandbox (an opaque origin with no
// script, so it can reach nothing of this site's), and anything that could
// run is a download rather than a page. Images, video and audio still
// display and embed as before.
func setBlobHeaders(ctx *gin.Context, contentType string) {
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'; style-src 'unsafe-inline'")
	ctx.Header("Cross-Origin-Resource-Policy", "cross-origin")
	if isActiveType(contentType) {
		ctx.Header("Content-Disposition", "attachment")
	}
}
