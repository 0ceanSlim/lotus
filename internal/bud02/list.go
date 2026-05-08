package bud02

import (
	"context"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

func ListBlobs(ctx context.Context, services core.Services, pubkey string) ([]*core.Blob, error) {
	return services.Blob().GetFromPubkey(ctx, pubkey)
}

type ListAllBlobsResult struct {
	Blobs []*core.Blob
	Total int64
}

func ListAllBlobs(
	ctx context.Context,
	queries *db.Queries,
	cdnBaseUrl string,
	limit int64,
	offset int64,
) (*ListAllBlobsResult, error) {
	dbBlobs, err := queries.GetAllBlobs(ctx, db.GetAllBlobsParams{
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, err
	}

	total, err := queries.GetTotalBlobsCount(ctx)
	if err != nil {
		return nil, err
	}

	blobs := make([]*core.Blob, len(dbBlobs))
	for i := range dbBlobs {
		url := cdnBaseUrl + "/" + dbBlobs[i].Hash
		blobs[i] = &core.Blob{
			Url:      url,
			Sha256:   dbBlobs[i].Hash,
			Size:     dbBlobs[i].Size,
			Type:     dbBlobs[i].Type,
			Uploaded: dbBlobs[i].Created,
			NIP94: &core.NIP94FileMetadata{
				Url:            url,
				MimeType:       dbBlobs[i].Type,
				OriginalSha256: dbBlobs[i].Hash,
				Sha256:         dbBlobs[i].Hash,
			},
		}
	}

	return &ListAllBlobsResult{
		Blobs: blobs,
		Total: total,
	}, nil
}
