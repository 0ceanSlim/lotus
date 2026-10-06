package bud02

import (
	"context"
	"errors"

	"github.com/0ceanSlim/lotus/internal/core"
)

func DeleteBlob(
	ctx context.Context,
	services core.Services,
	pubkey string,
	hash string,
	authHash string,
) error {
	blobs := services.Blob()
	blobDescriptor, err := blobs.GetFromHash(ctx, hash)
	if err != nil {
		return core.ErrBlobNotFound
	}

	if blobDescriptor.Pubkey != "" && blobDescriptor.Pubkey != pubkey {
		return errors.New("unauthorized: pubkey mismatch - blob owner: " + blobDescriptor.Pubkey + ", request: " + pubkey)
	}

	if hash != authHash {
		return errors.New("unauthorized: hash mismatch - url: " + hash + ", auth: " + authHash)
	}

	if err := blobs.DeleteFromHash(ctx, hash); err != nil {
		return err
	}

	// Short links to a removed blob would only ever 404; drop them with it.
	if links := services.ShortLinks(); links != nil {
		_ = links.DeleteForHash(ctx, hash)
	}

	return nil
}
