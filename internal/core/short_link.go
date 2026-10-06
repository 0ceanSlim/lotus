package core

import (
	"context"
	"errors"
)

var (
	ErrShortLinkNotFound = errors.New("short link not found")
	ErrNotBlobOwner      = errors.New("blob belongs to a different pubkey")
)

// ShortLink is an alias for a blob: /s/<code> redirects to the canonical
// sha256 URL. The canonical URL stays the Blossom contract; a short link is
// something a user creates for sharing and can revoke.
type ShortLink struct {
	Code    string
	Hash    string
	Pubkey  string
	Created int64
}

type ShortLinkService interface {
	// GetOrCreate returns the owner's existing short link for the blob, or
	// mints one.
	GetOrCreate(ctx context.Context, pubkey, hash string) (*ShortLink, error)
	Resolve(ctx context.Context, code string) (*ShortLink, error)
	ListForPubkey(ctx context.Context, pubkey string) ([]*ShortLink, error)
	// Revoke deletes a link; only its owner may.
	Revoke(ctx context.Context, pubkey, code string) error
	// DeleteForHash removes every link to a blob, used when the blob goes.
	DeleteForHash(ctx context.Context, hash string) error
}
