package bud02

import (
	"context"
	"errors"
	"testing"

	"github.com/0ceanSlim/lotus/internal/core"
)

// sha256("hello world")
const helloHash = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"

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

// memBlobs is a BlobStorage over a map with scripted space and quota answers.
type memBlobs struct {
	core.BlobStorage
	saved      map[string]*core.Blob
	spaceErr   error
	spaceCalls int
	quotaErr   error
}

func (m *memBlobs) GetFromHash(_ context.Context, sha string) (*core.Blob, error) {
	if b, ok := m.saved[sha]; ok {
		return b, nil
	}
	return nil, core.ErrBlobNotFound
}

func (m *memBlobs) ValidateStorageQuota(context.Context, string, int64) error { return m.quotaErr }

func (m *memBlobs) EnsureSpace(context.Context, int64) error {
	m.spaceCalls++
	return m.spaceErr
}

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

func TestUploadBlobStoresWhenEverythingAllows(t *testing.T) {
	svc := newTestServices()
	blob, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world"))
	if err != nil {
		t.Fatal(err)
	}
	if blob.Pubkey != ownerPubkey || blob.Sha256 != helloHash || blob.Size != 11 {
		t.Fatalf("stored %+v", blob)
	}
	if svc.blobs.spaceCalls != 1 {
		t.Fatalf("EnsureSpace called %d times, want 1", svc.blobs.spaceCalls)
	}
}

func TestUploadBlobRefusedWhenServerFull(t *testing.T) {
	svc := newTestServices()
	svc.blobs.spaceErr = core.ErrServerFull
	_, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world"))
	if !errors.Is(err, core.ErrServerFull) {
		t.Fatalf("got %v, want ErrServerFull", err)
	}
	if len(svc.blobs.saved) != 0 {
		t.Fatal("blob was stored on a full server")
	}
}

func TestUploadBlobDedupeNeedsNoSpace(t *testing.T) {
	svc := newTestServices()
	svc.blobs.saved[helloHash] = &core.Blob{Pubkey: otherPubkey, Sha256: helloHash}
	svc.blobs.spaceErr = core.ErrServerFull
	blob, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world"))
	if err != nil {
		t.Fatalf("an existing blob should be returned even on a full server: %v", err)
	}
	if blob.Pubkey != otherPubkey {
		t.Fatal("dedupe should return the first uploader's blob")
	}
	if svc.blobs.spaceCalls != 0 {
		t.Fatal("EnsureSpace was consulted for a blob that already exists")
	}
}

func TestUploadBlobChecksAccessQuotaAndSize(t *testing.T) {
	svc := newTestServices()
	svc.acr = allowACR{deny: ownerPubkey}
	if _, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world")); err == nil {
		t.Fatal("denied pubkey uploaded")
	}
	svc = newTestServices()
	svc.blobs.quotaErr = core.ErrStorageQuotaExceeded
	if _, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world")); !errors.Is(err, core.ErrStorageQuotaExceeded) {
		t.Fatalf("quota: got %v", err)
	}
	svc = newTestServices()
	svc.settings = sizeLimit{max: 5}
	if _, err := UploadBlob(context.Background(), svc, "http://cdn", helloHash, ownerPubkey, []byte("hello world")); !errors.Is(err, core.ErrFileSizeLimit) {
		t.Fatalf("size: got %v", err)
	}
	if len(svc.blobs.saved) != 0 {
		t.Fatal("a refused upload was stored")
	}
}
