package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
)

// denyAllServices lets a request reach bud02.UploadBlob and be refused
// there, proving the body passed the cap without needing real storage.
type denyAllServices struct{ core.Services }

type denyACR struct{}

func (denyACR) Validate(context.Context, string, core.ACRResource) error {
	return errors.New("unauthorized")
}

func (denyAllServices) Blob() core.BlobStorage        { return nil }
func (denyAllServices) Mime() core.MimeTypeService    { return nil }
func (denyAllServices) Settings() core.SettingService { return nil }
func (denyAllServices) ACR() core.ACRStorage          { return denyACR{} }

func storeWithMaxUpload(t *testing.T, max int) *config.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	src := "db_path: \"db/x.sqlite3\"\nlog_level: \"INFO\"\napi_addr: \"127.0.0.1:0\"\ncdn_url: \"http://localhost:9999\"\n" +
		"max_upload_size_bytes: " + itoa(max) + "\naccess_control_rules:\n  - action: \"ALLOW\"\n    pubkey: \"ALL\"\n    resource: \"GET\"\nallowed_mime_types:\n  - \"*\"\n"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	store, _, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func itoa(n int) string {
	return strings.TrimSpace(string(mustJSON(n)))
}

// chunkedBody hides its length so the request goes out without a
// Content-Length.
type chunkedBody struct{ io.Reader }

func TestUploadBodyIsCappedAtTheFileLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/upload", uploadBlob(denyAllServices{}, storeWithMaxUpload(t, 10), "http://localhost:9999"))

	send := func(body string, declare bool) *httptest.ResponseRecorder {
		var rd io.Reader = strings.NewReader(body)
		if !declare {
			rd = chunkedBody{rd}
		}
		req := httptest.NewRequest(http.MethodPut, "/upload", rd)
		if !declare {
			req.ContentLength = -1
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// Declared over the limit: refused on the header alone.
	w := send(strings.Repeat("x", 20), true)
	if w.Code != http.StatusRequestEntityTooLarge || !strings.Contains(w.Header().Get(HeaderXReason), "file size") {
		t.Fatalf("declared oversize: %d %q", w.Code, w.Header().Get(HeaderXReason))
	}
	// Undeclared over the limit: cut off at the limit.
	w = send(strings.Repeat("x", 20), false)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("chunked oversize: %d %s", w.Code, w.Body)
	}
	// Within the limit: reaches the domain code (which denies: 400, not 413).
	w = send(strings.Repeat("x", 10), true)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "unauthorized") {
		t.Fatalf("within limit: %d %s", w.Code, w.Body)
	}

	// No limit configured: nothing is capped.
	r = gin.New()
	r.PUT("/upload", uploadBlob(denyAllServices{}, storeWithMaxUpload(t, 0), "http://localhost:9999"))
	w = send(strings.Repeat("x", 20), true)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unlimited: %d", w.Code)
	}
}

func TestWriteBlobErrorStatuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[error]int{
		core.ErrServerFull:           http.StatusInsufficientStorage,
		core.ErrFileSizeLimit:        http.StatusRequestEntityTooLarge,
		core.ErrStorageQuotaExceeded: http.StatusBadRequest,
		errors.New("anything else"):  http.StatusBadRequest,
	}
	for err, want := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		writeBlobError(c, err)
		if w.Code != want {
			t.Errorf("%v: %d, want %d", err, w.Code, want)
		}
	}
}
