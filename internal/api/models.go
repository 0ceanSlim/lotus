package api

import (
	"fmt"

	"github.com/0ceanSlim/lotus/internal/core"
)

type apiError struct {
	Message string `json:"message"`
}

type blobDescriptor struct {
	Url      string     `json:"url"`
	Sha256   string     `json:"sha256"`
	Size     int64      `json:"size"`
	Type     string     `json:"type"`
	Uploaded int64      `json:"uploaded"`
	Pubkey   string     `json:"pubkey,omitempty"`
	NIP94    [][]string `json:"nip94,omitempty"`
}

func fromDomainBlobDescriptor(blob *core.Blob) *blobDescriptor {
	apiBlob := &blobDescriptor{
		Url:      blob.Url,
		Sha256:   blob.Sha256,
		Size:     blob.Size,
		Type:     blob.Type,
		Uploaded: blob.Uploaded,
		Pubkey:   blob.Pubkey,
	}

	if blob.NIP94 != nil {
		tags := [][]string{
			{"url", blob.NIP94.Url},
			{"m", blob.NIP94.MimeType},
			{"x", blob.NIP94.Sha256},
			{"ox", blob.NIP94.OriginalSha256},
			{"size", fmt.Sprintf("%d", blob.Size)},
		}
		if blob.NIP94.Dimension != nil {
			tags = append(tags, []string{"dim", *blob.NIP94.Dimension})
		}
		if blob.NIP94.Blurhash != nil {
			tags = append(tags, []string{"blurhash", *blob.NIP94.Blurhash})
		}
		if blob.NIP94.ThumbnailUrl != nil {
			tags = append(tags, []string{"thumb", *blob.NIP94.ThumbnailUrl})
		}
		if blob.NIP94.Magnet != nil {
			tags = append(tags, []string{"magnet", *blob.NIP94.Magnet})
		}
		if blob.NIP94.Infohash != nil {
			tags = append(tags, []string{"i", *blob.NIP94.Infohash})
		}
		if blob.NIP94.Summary != nil {
			tags = append(tags, []string{"summary", *blob.NIP94.Summary})
		}
		if blob.NIP94.Alt != nil {
			tags = append(tags, []string{"alt", *blob.NIP94.Alt})
		}
		apiBlob.NIP94 = tags
	}

	return apiBlob
}

func fromSliceDomainBlobDescriptor(blobs []*core.Blob) []*blobDescriptor {
	apiBlobs := make([]*blobDescriptor, len(blobs))
	for i := range blobs {
		apiBlobs[i] = fromDomainBlobDescriptor(blobs[i])
	}
	return apiBlobs
}

type mirrorInput struct {
	Url string `json:"url"`
}
