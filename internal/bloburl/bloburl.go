// Package bloburl builds the public URLs returned for blobs.
//
// Blob URLs carry the file extension that matches the blob's MIME type
// (e.g. /<sha256>.png). Some clients cannot determine how to render media
// unless the extension is present, so it is appended to every link the
// server hands back. The GET/HEAD/DELETE endpoints ignore the extension and
// resolve blobs by their bare hash, so the suffixed URL round-trips cleanly.
package bloburl

import (
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

// Build returns the public URL for a blob: cdnBaseURL + "/" + hash, with the
// file extension for mimeType appended when one is known.
func Build(cdnBaseURL, hash, mimeType string) string {
	return cdnBaseURL + "/" + hash + Extension(mimeType)
}

// Extension returns the canonical file extension (including the leading dot)
// for mimeType, or "" when the type is unknown or has no extension. Any MIME
// parameters such as "; charset=utf-8" are stripped before the lookup so that
// detected types like "text/plain; charset=utf-8" still resolve.
func Extension(mimeType string) string {
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = strings.TrimSpace(mimeType[:i])
	}
	if m := mimetype.Lookup(mimeType); m != nil {
		return m.Extension()
	}
	return ""
}
