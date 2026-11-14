package core

import (
	"context"
	"errors"
)

var (
	ErrBlobNotFound         = errors.New("blob not found")
	ErrStorageQuotaExceeded = errors.New("storage quota exceeded")
	// ErrServerFull means the overall storage cap is reached and nothing
	// could (or may) be evicted to make room.
	ErrServerFull = errors.New("server storage is full")
)

// StoragePolicy is the overall cap on stored bytes and what happens at it.
// A zero MaxTotalBytes means no cap. With Evict, the blobs with the largest
// age × size are deleted to make room for a new one; otherwise the upload
// is refused.
type StoragePolicy struct {
	MaxTotalBytes int64
	Evict         bool
}

type Blob struct {
	Pubkey      string
	Url         string
	ExternalUrl string // internal storage URL (e.g. 0x0.st), empty in normal mode
	Sha256      string
	Size        int64
	Type        string
	Blob        []byte
	Uploaded    int64
	NIP94       *NIP94FileMetadata
}

type NIP94FileMetadata struct {
	Url            string
	MimeType       string
	Sha256         string
	OriginalSha256 string
	Size           *int64
	Dimension      *string
	Magnet         *string
	Infohash       *string
	Blurhash       *string
	ThumbnailUrl   *string
	ImageUrl       *string
	Summary        *string
	Alt            *string
	Fallback       *string
	Service        *string
}

type BlobStorage interface {
	Save(
		ctx context.Context,
		pubkey string,
		sha256 string,
		url string,
		size int64,
		mimeType string,
		blob []byte,
		created int64,
	) (*Blob, error)
	Exists(ctx context.Context, sha256 string) (bool, error)
	GetFromHash(ctx context.Context, sha256 string) (*Blob, error)
	GetFromPubkey(ctx context.Context, pubkey string) ([]*Blob, error)
	// ListMeta returns a pubkey's blobs newest first without loading blob
	// bytes; the drive listing uses it.
	ListMeta(ctx context.Context, pubkey string) ([]*Blob, error)
	// Meta returns one blob's metadata without its bytes.
	Meta(ctx context.Context, sha256 string) (*Blob, error)
	DeleteFromHash(ctx context.Context, sha256 string) error
	ValidateStorageQuota(ctx context.Context, pubkey string, newFileSize int64) error
	// EnsureSpace makes sure size more bytes fit under the overall storage
	// cap, evicting older blobs when the policy allows, or returns
	// ErrServerFull.
	EnsureSpace(ctx context.Context, size int64) error
}
