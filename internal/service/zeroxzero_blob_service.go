package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/bloburl"
	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/retention"
	"github.com/0ceanSlim/lotus/internal/zeroxzero"
)

type zeroXZeroBlobService struct {
	storageGovernor
	db                       *sql.DB
	queries                  *db.Queries
	client                   *zeroxzero.Client
	conf                     config.ZeroXZeroConfig
	cdnBaseUrl               string
	maxStoragePerPubkeyBytes atomic.Int64
	// retention, when set, chooses and quotes each file's expiry; without
	// it the fixed 0x0 curve from zero_x_zero applies.
	retention *retention.Controller
	log       *zap.Logger
}

// SetRetention hands the service the controller that picks expiries.
func (s *zeroXZeroBlobService) SetRetention(c *retention.Controller) { s.retention = c }

func NewZeroXZeroBlobService(
	database *sql.DB,
	queries *db.Queries,
	conf *config.Config,
	log *zap.Logger,
) (core.BlobStorage, error) {
	s := &zeroXZeroBlobService{
		db:         database,
		queries:    queries,
		client:     zeroxzero.New(conf.ZeroXZero.InstanceUrl),
		conf:       conf.ZeroXZero,
		cdnBaseUrl: conf.CdnUrl,
		log:        log,
	}
	s.storageGovernor.log = log
	s.SetQuota(conf.MaxStoragePerPubkeyBytes)
	return s, nil
}

// SetQuota changes the per-pubkey storage quota; zero disables it.
func (s *zeroXZeroBlobService) SetQuota(bytes int64) {
	s.maxStoragePerPubkeyBytes.Store(bytes)
}

// EnsureSpace enforces the overall cap over the blobs_0x0 table. Evicting
// deletes from the 0x0 backend as well.
func (s *zeroXZeroBlobService) EnsureSpace(ctx context.Context, size int64) error {
	return s.ensureSpace(ctx, size, s.queries.GetTotalBlob0x0Bytes, s.queries.ListEviction0x0Candidates, s.evict)
}

func (s *zeroXZeroBlobService) evict(ctx context.Context, hash string) error {
	if err := s.DeleteFromHash(ctx, hash); err != nil {
		return err
	}
	return s.queries.DeleteShortLinksByHash(ctx, hash)
}

func (s *zeroXZeroBlobService) Save(
	ctx context.Context,
	pubkey string,
	sha256 string,
	_ string,
	size int64,
	mimeType string,
	blob []byte,
	created int64,
) (*core.Blob, error) {
	if existing, err := s.queries.GetBlob0x0FromHash(ctx, sha256); err == nil {
		return s.Renew(ctx, s.dbBlobToDescriptor(existing))
	}

	now := time.Now()
	var expiry int64
	if s.retention != nil {
		s.retention.Offer(size)
		if err := s.retention.Admit(size); err != nil {
			return nil, core.ErrServerFull
		}
		expiry = s.retention.ExpiryFor(sha256, size, now)
	} else {
		expiry = s.calculateExpiry(size)
	}

	stored, err := s.client.Upload(blob, sha256, expiry)
	if err != nil {
		return nil, fmt.Errorf("0x0 upload: %w", err)
	}
	// The backend rounds a request down to its own ceiling; promise only
	// what it will actually keep.
	if stored.ExpiresAt > 0 && stored.ExpiresAt < expiry {
		s.log.Warn("0x0 backend shortened an expiry; raise FHOST_MAX_EXPIRATION to at least retention.ttl_max",
			zap.Int64("asked", expiry), zap.Int64("got", stored.ExpiresAt))
		expiry = stored.ExpiresAt
	}

	row, err := s.queries.InsertBlob0x0(ctx, db.InsertBlob0x0Params{
		Hash:    sha256,
		Pubkey:  pubkey,
		Url:     stored.Url,
		Size:    size,
		Type:    mimeType,
		Expiry:  sql.NullInt64{Int64: expiry, Valid: true},
		Created: created,
	})
	if err != nil {
		return nil, fmt.Errorf("insert blob_0x0: %w", err)
	}
	if stored.Token != "" {
		if err := s.queries.SetBlob0x0Token(ctx, stored.Token, sha256); err != nil {
			s.log.Error("could not record 0x0 management token", zap.Error(err))
		}
	}
	if s.retention != nil {
		s.retention.Commit(size)
	}

	return s.dbBlobToDescriptor(row), nil
}

// Renew is called when a file that is already stored is uploaded again: its
// expiry becomes the later of the current one and what a new upload would
// get now, on the backend as well.
func (s *zeroXZeroBlobService) Renew(ctx context.Context, blob *core.Blob) (*core.Blob, error) {
	if s.retention == nil {
		return blob, nil
	}
	next := s.retention.ExpiryFor(blob.Sha256, blob.Size, time.Now())
	if next <= blob.Expires {
		return blob, nil
	}
	row, err := s.queries.GetBlob0x0FromHash(ctx, blob.Sha256)
	if err != nil {
		return blob, nil
	}
	token, _ := s.queries.GetBlob0x0Token(ctx, blob.Sha256)
	if err := s.client.SetExpiry(row.Url, token, next); err != nil {
		// Without the backend's agreement the old expiry stands.
		return blob, nil
	}
	if err := s.queries.UpdateBlob0x0Expiry(ctx, sql.NullInt64{Int64: next, Valid: true}, blob.Sha256); err != nil {
		return blob, nil
	}
	row.Expiry = sql.NullInt64{Int64: next, Valid: true}
	return s.dbBlobToDescriptor(row), nil
}

func (s *zeroXZeroBlobService) Exists(ctx context.Context, sha256 string) (bool, error) {
	_, err := s.queries.GetBlob0x0FromHash(ctx, sha256)
	return err == nil, err
}

func (s *zeroXZeroBlobService) GetFromHash(ctx context.Context, sha256 string) (*core.Blob, error) {
	row, err := s.queries.GetBlob0x0FromHash(ctx, sha256)
	if err != nil {
		return nil, core.ErrBlobNotFound
	}
	return s.dbBlobToDescriptor(row), nil
}

func (s *zeroXZeroBlobService) GetFromPubkey(ctx context.Context, pubkey string) ([]*core.Blob, error) {
	rows, err := s.queries.GetBlobs0x0FromPubkey(ctx, pubkey)
	if err != nil {
		return nil, err
	}
	blobs := make([]*core.Blob, len(rows))
	for i, row := range rows {
		blobs[i] = s.dbBlobToDescriptor(row)
	}
	return blobs, nil
}

func (s *zeroXZeroBlobService) Meta(ctx context.Context, sha256 string) (*core.Blob, error) {
	return s.GetFromHash(ctx, sha256)
}

func (s *zeroXZeroBlobService) ListMeta(ctx context.Context, pubkey string) ([]*core.Blob, error) {
	return s.GetFromPubkey(ctx, pubkey)
}

func (s *zeroXZeroBlobService) DeleteFromHash(ctx context.Context, sha256 string) error {
	row, err := s.queries.GetBlob0x0FromHash(ctx, sha256)
	if err == nil {
		token, _ := s.queries.GetBlob0x0Token(ctx, sha256)
		if err := s.client.Delete(row.Url, token); err != nil {
			if errors.Is(err, zeroxzero.ErrNoToken) {
				// Uploaded before tokens were recorded: the backend keeps
				// the bytes until its own expiry.
				s.log.Warn("0x0 file has no management token; the backend keeps it until it expires", zap.String("hash", sha256))
			} else {
				return fmt.Errorf("0x0 delete: %w", err)
			}
		}
		if s.retention != nil {
			s.retention.Release(row.Size)
		}
	}
	return s.queries.DeleteBlob0x0FromHash(ctx, sha256)
}

// ValidateStorageQuota applies max_storage_per_pubkey_bytes to the bytes a
// pubkey has recorded here. Rows expire with the files, so the sum shrinks
// as the backend forgets them.
func (s *zeroXZeroBlobService) ValidateStorageQuota(ctx context.Context, pubkey string, newFileSize int64) error {
	quota := s.maxStoragePerPubkeyBytes.Load()
	if quota <= 0 {
		return nil
	}
	current, err := s.queries.GetTotalStorage0x0ByPubkey(ctx, pubkey)
	if err != nil {
		return err
	}
	if current+newFileSize > quota {
		return core.ErrStorageQuotaExceeded
	}
	return nil
}

func (s *zeroXZeroBlobService) dbBlobToDescriptor(row db.Blobs0x0) *core.Blob {
	publicUrl := bloburl.Build(s.cdnBaseUrl, row.Hash, row.Type)
	return &core.Blob{
		Pubkey:      row.Pubkey,
		Url:         publicUrl,
		ExternalUrl: row.Url,
		Sha256:      row.Hash,
		Size:        row.Size,
		Type:        row.Type,
		Uploaded:    row.Created,
		Expires:     row.Expiry.Int64,
		NIP94: &core.NIP94FileMetadata{
			Url:            publicUrl,
			MimeType:       row.Type,
			OriginalSha256: row.Hash,
			Sha256:         row.Hash,
		},
	}
}

// calculateExpiry mirrors the 0x0.st retention curve:
// retention = min_days + (-max_days + min_days) * ((size/max_size) - 1)^3
func (s *zeroXZeroBlobService) calculateExpiry(sizeBytes int64) int64 {
	maxSize := s.conf.MaxFileSizeBytes
	if maxSize <= 0 {
		maxSize = 536870912
	}
	minDays := s.conf.MinRetentionDays
	if minDays <= 0 {
		minDays = 30
	}
	maxDays := s.conf.MaxRetentionDays
	if maxDays <= 0 {
		maxDays = 365
	}

	ratio := float64(sizeBytes)/float64(maxSize) - 1
	days := minDays + (-maxDays+minDays)*math.Pow(ratio, 3)
	if days < minDays {
		days = minDays
	}

	return time.Now().Add(time.Duration(days * 24 * float64(time.Hour))).Unix()
}
