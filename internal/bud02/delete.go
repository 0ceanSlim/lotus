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

	return nil
}
