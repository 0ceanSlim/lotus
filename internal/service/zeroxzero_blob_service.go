package service

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"go.uber.org/zap"

	"github.com/0ceanSlim/lotus/internal/bloburl"
	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
	"github.com/0ceanSlim/lotus/internal/zeroxzero"
)

type zeroXZeroBlobService struct {
	db         *sql.DB
	queries    *db.Queries
	client     *zeroxzero.Client
	conf       config.ZeroXZeroConfig
	cdnBaseUrl string
	log        *zap.Logger
}

func NewZeroXZeroBlobService(
	database *sql.DB,
	queries *db.Queries,
	conf *config.Config,
	log *zap.Logger,
) (core.BlobStorage, error) {
	return &zeroXZeroBlobService{
		db:         database,
		queries:    queries,
		client:     zeroxzero.New(conf.ZeroXZero.InstanceUrl),
		conf:       conf.ZeroXZero,
		cdnBaseUrl: conf.CdnUrl,
		log:        log,
	}, nil
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
		return s.dbBlobToDescriptor(existing), nil
	}

	fileUrl, err := s.client.Upload(blob, sha256)
	if err != nil {
		return nil, fmt.Errorf("0x0 upload: %w", err)
	}

	expiry := s.calculateExpiry(size)

	row, err := s.queries.InsertBlob0x0(ctx, db.InsertBlob0x0Params{
		Hash:    sha256,
		Pubkey:  pubkey,
		Url:     fileUrl,
		Size:    size,
		Type:    mimeType,
		Expiry:  sql.NullInt64{Int64: expiry, Valid: true},
		Created: created,
	})
	if err != nil {
		return nil, fmt.Errorf("insert blob_0x0: %w", err)
	}

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

func (s *zeroXZeroBlobService) DeleteFromHash(ctx context.Context, sha256 string) error {
	row, err := s.queries.GetBlob0x0FromHash(ctx, sha256)
	if err == nil {
		_ = s.client.Delete(row.Url)
	}
	return s.queries.DeleteBlob0x0FromHash(ctx, sha256)
}

func (s *zeroXZeroBlobService) ValidateStorageQuota(_ context.Context, _ string, _ int64) error {
	return nil
}

func (s *zeroXZeroBlobService) dbBlobToDescriptor(row db.Blobs0x0) *core.Blob {
	publicUrl := bloburl.Build(s.cdnBaseUrl, row.Hash, row.Type)
	return &core.Blob{
		Url:         publicUrl,
		ExternalUrl: row.Url,
		Sha256:      row.Hash,
		Size:        row.Size,
		Type:        row.Type,
		Uploaded:    row.Created,
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
