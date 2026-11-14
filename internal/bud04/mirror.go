package bud04

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/0ceanSlim/lotus/internal/bloburl"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/hashing"
	"github.com/gabriel-vasile/mimetype"
)

// mirrorTimeout bounds the whole fetch of one remote blob.
const mirrorTimeout = 60 * time.Second

// mirrorClient only connects to public addresses; see safeclient.go.
var mirrorClient = newMirrorClient(mirrorTimeout)

// MirrorBlob fetches a blob from another server and stores it under pubkey
// exactly as if it had been uploaded: the same access rule, MIME list,
// per-file size limit, per-pubkey quota and overall storage cap apply. The
// fetch never reads more than the size limit and refuses private, loopback
// and otherwise non-public targets, so a caller cannot use the server to
// reach things on its own network.
func MirrorBlob(
	ctx context.Context,
	services core.Services,
	cdnBaseUrl string,
	pubkey string,
	authHash string,
	blobUrl url.URL,
) (*core.Blob, error) {
	var (
		blobs    = services.Blob()
		mimes    = services.Mime()
		settings = services.Settings()
	)

	if err := services.ACR().Validate(ctx, pubkey, core.ResourceUpload); err != nil {
		return nil, err
	}

	if blob, err := blobs.GetFromHash(ctx, authHash); err == nil {
		return blob, nil
	}

	if (blobUrl.Scheme != "http" && blobUrl.Scheme != "https") || blobUrl.Host == "" {
		return nil, errors.New("mirror URL must be an http or https URL")
	}

	maxBytes := int64(settings.MaxUploadSizeBytes())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobUrl.String(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("GET blob at: %s: %w", blobUrl.String(), err)
	}
	res, err := mirrorClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET blob at: %s: %w", blobUrl.String(), err)
	}
	defer res.Body.Close()
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("GET blob at: %s returned HTTP status: %d", blobUrl.String(), res.StatusCode)
	}
	if maxBytes > 0 && res.ContentLength > maxBytes {
		return nil, fmt.Errorf("file size: %w", core.ErrFileSizeLimit)
	}

	var body io.Reader = res.Body
	if maxBytes > 0 {
		body = io.LimitReader(res.Body, maxBytes+1)
	}
	blobBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("GET blob at: %s: read body: %w", blobUrl.String(), err)
	}
	if maxBytes > 0 && int64(len(blobBytes)) > maxBytes {
		return nil, fmt.Errorf("file size: %w", core.ErrFileSizeLimit)
	}

	mimeType := mimetype.Detect(blobBytes)
	if err := mimes.IsAllowed(ctx, mimeType.String()); err != nil {
		return nil, fmt.Errorf("mime type %s not allowed", mimeType.String())
	}

	if err := settings.ValidateFileSizeMaxBytes(ctx, len(blobBytes)); err != nil {
		return nil, fmt.Errorf("file size: %w", err)
	}

	if err := blobs.ValidateStorageQuota(ctx, pubkey, int64(len(blobBytes))); err != nil {
		return nil, fmt.Errorf("storage quota: %w", err)
	}

	hash, err := hashing.Hash(blobBytes)
	if err != nil {
		return nil, fmt.Errorf("hash blob: %w", err)
	}

	if hash != authHash {
		return nil, errors.New("hash from auth doesn't match hash from blob")
	}

	if err := blobs.EnsureSpace(ctx, int64(len(blobBytes))); err != nil {
		return nil, err
	}

	blobDescriptor, err := blobs.Save(
		ctx,
		pubkey,
		hash,
		bloburl.Build(cdnBaseUrl, hash, mimeType.String()),
		int64(len(blobBytes)),
		mimeType.String(),
		blobBytes,
		time.Now().Unix(),
	)
	if err != nil {
		return nil, fmt.Errorf("save blob: %w", err)
	}

	return blobDescriptor, nil
}
