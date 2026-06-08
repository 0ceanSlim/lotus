package bloburl

import "testing"

func TestExtension(t *testing.T) {
	cases := map[string]string{
		"image/png":                 ".png",
		"image/jpeg":                ".jpg",
		"image/svg+xml":             ".svg",
		"image/webp":                ".webp",
		"video/mp4":                 ".mp4",
		"audio/mpeg":                ".mp3",
		"application/pdf":           ".pdf",
		"text/plain; charset=utf-8": ".txt",
		"text/html; charset=utf-8":  ".html",
		"application/octet-stream":  "", // no canonical extension
		"application/x-not-real":    "", // unknown type
		"":                          "",
	}
	for mime, want := range cases {
		if got := Extension(mime); got != want {
			t.Errorf("Extension(%q) = %q, want %q", mime, got, want)
		}
	}
}

func TestBuild(t *testing.T) {
	const (
		cdn  = "https://cdn.example.com"
		hash = "abc123"
	)
	cases := map[string]string{
		"image/png":                 "https://cdn.example.com/abc123.png",
		"text/plain; charset=utf-8": "https://cdn.example.com/abc123.txt",
		"application/octet-stream":  "https://cdn.example.com/abc123", // unknown -> bare hash
	}
	for mime, want := range cases {
		if got := Build(cdn, hash, mime); got != want {
			t.Errorf("Build(_, _, %q) = %q, want %q", mime, got, want)
		}
	}
}
