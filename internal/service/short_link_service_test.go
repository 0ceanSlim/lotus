package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

const (
	testOwner = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOther = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testHash  = "6fe9e497859ad682ca2acdc93c891a8baa2fe10489d295497a5952b76242fbed"
)

func newTestShortLinks(t *testing.T) core.ShortLinkService {
	t.Helper()
	database, err := db.NewDB(filepath.Join(t.TempDir(), "test.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return NewShortLinkService(db.New(database))
}

func TestShortLinkGetOrCreateIsIdempotentPerOwner(t *testing.T) {
	svc := newTestShortLinks(t)
	ctx := context.Background()

	first, err := svc.GetOrCreate(ctx, testOwner, testHash)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Code) != shortCodeLength {
		t.Fatalf("unexpected code %q", first.Code)
	}
	for _, c := range first.Code {
		if !contains(shortCodeAlphabet, c) {
			t.Fatalf("code %q uses a character outside the alphabet", first.Code)
		}
	}

	again, err := svc.GetOrCreate(ctx, testOwner, testHash)
	if err != nil {
		t.Fatal(err)
	}
	if again.Code != first.Code {
		t.Fatalf("expected the same code for the same owner and blob, got %s and %s", first.Code, again.Code)
	}

	other, err := svc.GetOrCreate(ctx, testOther, testHash)
	if err != nil {
		t.Fatal(err)
	}
	if other.Code == first.Code {
		t.Fatal("a different owner should get their own code")
	}

	resolved, err := svc.Resolve(ctx, first.Code)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Hash != testHash || resolved.Pubkey != testOwner {
		t.Fatalf("resolved wrong link: %+v", resolved)
	}
}

func TestShortLinkRevokeRequiresOwner(t *testing.T) {
	svc := newTestShortLinks(t)
	ctx := context.Background()
	link, err := svc.GetOrCreate(ctx, testOwner, testHash)
	if err != nil {
		t.Fatal(err)
	}

	if err := svc.Revoke(ctx, testOther, link.Code); !errors.Is(err, core.ErrShortLinkNotFound) {
		t.Fatalf("expected a non-owner revoke to fail, got %v", err)
	}
	if _, err := svc.Resolve(ctx, link.Code); err != nil {
		t.Fatal("link vanished after a rejected revoke")
	}

	if err := svc.Revoke(ctx, testOwner, link.Code); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Resolve(ctx, link.Code); !errors.Is(err, core.ErrShortLinkNotFound) {
		t.Fatalf("expected the link to be gone, got %v", err)
	}
}

func TestShortLinkDeleteForHashRemovesEveryOwnersLink(t *testing.T) {
	svc := newTestShortLinks(t)
	ctx := context.Background()
	a, _ := svc.GetOrCreate(ctx, testOwner, testHash)
	b, _ := svc.GetOrCreate(ctx, testOther, testHash)
	if err := svc.DeleteForHash(ctx, testHash); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{a.Code, b.Code} {
		if _, err := svc.Resolve(ctx, code); !errors.Is(err, core.ErrShortLinkNotFound) {
			t.Fatalf("expected %s to be gone, got %v", code, err)
		}
	}
	links, err := svc.ListForPubkey(ctx, testOwner)
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 0 {
		t.Fatalf("expected no links left, got %d", len(links))
	}
}

func contains(alphabet string, c rune) bool {
	for _, a := range alphabet {
		if a == c {
			return true
		}
	}
	return false
}
