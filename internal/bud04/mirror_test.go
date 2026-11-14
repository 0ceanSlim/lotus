package bud04

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/0ceanSlim/lotus/internal/core"
)

const (
	ownerPubkey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// sha256("hello world")
	helloHash = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
)

type allowACR struct{ deny string }

func (a allowACR) Validate(_ context.Context, pubkey string, _ core.ACRResource) error {
	if pubkey == a.deny {
		return errors.New("unauthorized")
	}
	return nil
}

type anyMime struct{}

func (anyMime) Get(context.Context, string) (*core.MimeType, error) { return nil, nil }
func (anyMime) IsAllowed(context.Context, string) error             { return nil }

type sizeLimit struct{ max int }

func (s sizeLimit) ValidateFileSizeMaxBytes(_ context.Context, n int) error {
	if s.max > 0 && n > s.max {
		return core.ErrFileSizeLimit
	}
	return nil
}
func (s sizeLimit) MaxUploadSizeBytes() int { return s.max }

type memBlobs struct {
	core.BlobStorage
	saved    map[string]*core.Blob
	spaceErr error
	quotaErr error
}

func (m *memBlobs) GetFromHash(_ context.Context, sha string) (*core.Blob, error) {
	if b, ok := m.saved[sha]; ok {
		return b, nil
	}
	return nil, core.ErrBlobNotFound
}
func (m *memBlobs) ValidateStorageQuota(context.Context, string, int64) error { return m.quotaErr }
func (m *memBlobs) EnsureSpace(context.Context, int64) error                  { return m.spaceErr }
func (m *memBlobs) Save(_ context.Context, pubkey, sha, url string, size int64, mime string, _ []byte, created int64) (*core.Blob, error) {
	b := &core.Blob{Pubkey: pubkey, Sha256: sha, Url: url, Size: size, Type: mime, Uploaded: created}
	m.saved[sha] = b
	return b, nil
}

type testServices struct {
	core.Services
	blobs    *memBlobs
	acr      allowACR
	settings sizeLimit
}

func (s *testServices) Blob() core.BlobStorage        { return s.blobs }
func (s *testServices) ACR() core.ACRStorage          { return s.acr }
func (s *testServices) Mime() core.MimeTypeService    { return anyMime{} }
func (s *testServices) Settings() core.SettingService { return s.settings }

func newTestServices() *testServices {
	return &testServices{blobs: &memBlobs{saved: map[string]*core.Blob{}}}
}

// allowLoopback lets a test reach an httptest server, which lives on
// 127.0.0.1 and would otherwise be refused.
func allowLoopback(t *testing.T) {
	t.Helper()
	prev := dialCheck
	dialCheck = func(net.IP) bool { return true }
	t.Cleanup(func() { dialCheck = prev })
}

func mustURL(t *testing.T, s string) url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return *u
}

func TestIsPublicIP(t *testing.T) {
	private := []string{"127.0.0.1", "10.1.2.3", "172.16.5.5", "192.168.1.1", "169.254.1.1", "100.64.0.1", "0.0.0.0", "224.0.0.1", "::1", "::", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "::ffff:10.0.0.1", "64:ff9b::a00:1", "2002:0a00:0001::"}
	for _, s := range private {
		if isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should not be public", s)
		}
	}
	public := []string{"1.1.1.1", "8.8.8.8", "93.184.216.34", "2606:4700:4700::1111", "2a00:1450:4001:80e::200e"}
	for _, s := range public {
		if !isPublicIP(net.ParseIP(s)) {
			t.Errorf("%s should be public", s)
		}
	}
	if isPublicIP(nil) {
		t.Error("nil should not be public")
	}
}

func TestMirrorRefusesNonPublicTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()
	svc := newTestServices()
	_, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, srv.URL+"/x"))
	if err == nil || !strings.Contains(err.Error(), "non-public") {
		t.Fatalf("a loopback target should be refused, got %v", err)
	}
	if len(svc.blobs.saved) != 0 {
		t.Fatal("blob stored from a refused target")
	}
	for _, bad := range []string{"ftp://example.com/x", "file:///etc/passwd", "http:///nohost"} {
		if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, bad)); err == nil {
			t.Errorf("%s should be refused", bad)
		}
	}
}

func TestMirrorStoresLikeAnUpload(t *testing.T) {
	allowLoopback(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	svc := newTestServices()
	blob, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, srv.URL+"/x"))
	if err != nil {
		t.Fatal(err)
	}
	if blob.Pubkey != ownerPubkey || blob.Size != 11 || blob.Sha256 != helloHash {
		t.Fatalf("stored %+v", blob)
	}

	// Access rules apply to mirror exactly as to upload.
	svc = newTestServices()
	svc.acr = allowACR{deny: ownerPubkey}
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, srv.URL+"/x")); err == nil {
		t.Fatal("denied pubkey mirrored a blob")
	}
	// So does the per-pubkey quota and the overall cap.
	svc = newTestServices()
	svc.blobs.quotaErr = core.ErrStorageQuotaExceeded
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, srv.URL+"/x")); !errors.Is(err, core.ErrStorageQuotaExceeded) {
		t.Fatalf("quota: %v", err)
	}
	svc = newTestServices()
	svc.blobs.spaceErr = core.ErrServerFull
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, srv.URL+"/x")); !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("full: %v", err)
	}
	// A wrong hash is refused after the fetch.
	svc = newTestServices()
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, strings.Repeat("0", 64), mustURL(t, srv.URL+"/x")); err == nil {
		t.Fatal("hash mismatch accepted")
	}
}

func TestMirrorNeverReadsPastTheSizeLimit(t *testing.T) {
	allowLoopback(t)
	// Declared size: refused on the headers alone.
	declared := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 100)))
	}))
	defer declared.Close()
	svc := newTestServices()
	svc.settings = sizeLimit{max: 10}
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, declared.URL+"/x")); !errors.Is(err, core.ErrFileSizeLimit) {
		t.Fatalf("declared oversize: %v", err)
	}

	// Chunked, no Content-Length: the read stops at the limit.
	var sent int
	chunked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		for range 1000 {
			if _, err := w.Write([]byte(strings.Repeat("y", 1024))); err != nil {
				return
			}
			sent += 1024
			w.(http.Flusher).Flush()
		}
	}))
	defer chunked.Close()
	svc = newTestServices()
	svc.settings = sizeLimit{max: 10}
	if _, err := MirrorBlob(context.Background(), svc, "http://cdn", ownerPubkey, helloHash, mustURL(t, chunked.URL+"/x")); !errors.Is(err, core.ErrFileSizeLimit) {
		t.Fatalf("chunked oversize: %v", err)
	}
	if sent >= 1000*1024 {
		t.Fatal("the whole chunked body was consumed")
	}
}
