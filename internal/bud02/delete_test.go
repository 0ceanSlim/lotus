package bud02

import (
	"context"
	"testing"

	"github.com/0ceanSlim/lotus/internal/core"
)

// fakeBlobs is a BlobStorage with one blob owned by ownerPubkey.
type fakeBlobs struct {
	core.BlobStorage
	owner   string
	hash    string
	deleted bool
}

func (f *fakeBlobs) GetFromHash(_ context.Context, sha256 string) (*core.Blob, error) {
	if sha256 != f.hash {
		return nil, core.ErrBlobNotFound
	}
	return &core.Blob{Pubkey: f.owner, Sha256: f.hash}, nil
}

func (f *fakeBlobs) DeleteFromHash(_ context.Context, sha256 string) error {
	f.deleted = true
	return nil
}

type fakeServices struct {
	core.Services
	blobs core.BlobStorage
}

func (s *fakeServices) Blob() core.BlobStorage            { return s.blobs }
func (s *fakeServices) ShortLinks() core.ShortLinkService { return nil }

const (
	ownerPubkey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	otherPubkey = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	blobHash    = "6fe9e497859ad682ca2acdc93c891a8baa2fe10489d295497a5952b76242fbed"
)

func TestDeleteBlobRejectsOtherPubkey(t *testing.T) {
	blobs := &fakeBlobs{owner: ownerPubkey, hash: blobHash}
	svc := &fakeServices{blobs: blobs}
	if err := DeleteBlob(context.Background(), svc, otherPubkey, blobHash, blobHash); err == nil {
		t.Fatal("expected a delete signed by a different pubkey to be rejected")
	}
	if blobs.deleted {
		t.Fatal("blob was deleted despite the ownership mismatch")
	}
}

func TestDeleteBlobAllowsOwner(t *testing.T) {
	blobs := &fakeBlobs{owner: ownerPubkey, hash: blobHash}
	svc := &fakeServices{blobs: blobs}
	if err := DeleteBlob(context.Background(), svc, ownerPubkey, blobHash, blobHash); err != nil {
		t.Fatalf("expected the owner to delete, got %v", err)
	}
	if !blobs.deleted {
		t.Fatal("owner delete did not reach storage")
	}
}

func TestDeleteBlobRejectsAuthHashMismatch(t *testing.T) {
	blobs := &fakeBlobs{owner: ownerPubkey, hash: blobHash}
	svc := &fakeServices{blobs: blobs}
	if err := DeleteBlob(context.Background(), svc, ownerPubkey, blobHash, "00"); err == nil {
		t.Fatal("expected a delete whose auth x tag names another hash to be rejected")
	}
	if blobs.deleted {
		t.Fatal("blob was deleted despite the auth hash mismatch")
	}
}
