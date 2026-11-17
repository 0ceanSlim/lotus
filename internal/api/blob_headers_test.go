package api

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestIsActiveType(t *testing.T) {
	active := []string{"text/html; charset=utf-8", "image/svg+xml", "application/xhtml+xml", "text/xml", "application/vnd.foo+xml", "TEXT/HTML", "text/javascript"}
	inert := []string{"image/png", "image/jpeg", "video/mp4", "text/plain; charset=utf-8", "application/octet-stream", "application/pdf", ""}
	for _, ct := range active {
		if !isActiveType(ct) {
			t.Errorf("%q should be active", ct)
		}
	}
	for _, ct := range inert {
		if isActiveType(ct) {
			t.Errorf("%q should be inert", ct)
		}
	}
}

func TestSetBlobHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		ct         string
		attachment bool
	}{{"text/html; charset=utf-8", true}, {"image/svg+xml", true}, {"image/png", false}} {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		setBlobHeaders(ctx, tc.ct)
		h := w.Header()
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", tc.ct)
		}
		if h.Get("Content-Security-Policy") == "" {
			t.Errorf("%s: missing CSP", tc.ct)
		}
		if got := h.Get("Content-Disposition") == "attachment"; got != tc.attachment {
			t.Errorf("%s: attachment=%v, want %v", tc.ct, got, tc.attachment)
		}
	}
}
