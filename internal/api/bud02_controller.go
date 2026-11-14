package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/bud02"
	"github.com/0ceanSlim/lotus/internal/config"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

// uploadBlob stores a signed upload. The request body is capped at the
// per-file limit before it is read, so an oversize or endless body is cut
// off at the limit instead of being buffered whole; a declared
// Content-Length over the limit is refused without reading at all.
func uploadBlob(services core.Services, store *config.Store, cdnBaseUrl string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if max := store.Get().MaxUploadSizeBytes; max > 0 {
			if ctx.Request.ContentLength > int64(max) {
				refuse(ctx, http.StatusRequestEntityTooLarge, "file size: "+core.ErrFileSizeLimit.Error())
				return
			}
			ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, int64(max))
		}

		bodyBytes, err := io.ReadAll(ctx.Request.Body)
		defer ctx.Request.Body.Close()
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				refuse(ctx, http.StatusRequestEntityTooLarge, "file size: "+core.ErrFileSizeLimit.Error())
				return
			}
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{
				Message: fmt.Sprintf("failed to read request body: %s", err.Error()),
			})
			return
		}

		blobDescriptor, err := bud02.UploadBlob(
			ctx.Request.Context(),
			services,
			cdnBaseUrl,
			ctx.GetString("x"),
			ctx.GetString("pk"),
			bodyBytes,
		)
		if err != nil {
			writeBlobError(ctx, err)
			return
		}

		ctx.JSON(http.StatusOK, fromDomainBlobDescriptor(blobDescriptor))
	}
}

// writeBlobError maps a storing error to its status: 507 when the server is
// full, 413 when the file is over the per-file limit, 400 for everything
// else (as before).
func writeBlobError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, core.ErrServerFull):
		refuse(ctx, http.StatusInsufficientStorage, err.Error())
	case errors.Is(err, core.ErrFileSizeLimit):
		refuse(ctx, http.StatusRequestEntityTooLarge, err.Error())
	default:
		ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: err.Error()})
	}
}

// refuse answers with a BUD-style X-Reason header and the JSON message.
func refuse(ctx *gin.Context, status int, reason string) {
	ctx.Header(HeaderXReason, reason)
	ctx.AbortWithStatusJSON(status, apiError{Message: reason})
}

func listBlobs(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		blobs, err := bud02.ListBlobs(ctx.Request.Context(), services, ctx.Param("pubkey"))
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, fromSliceDomainBlobDescriptor(blobs))
	}
}

func deleteBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		hash := strings.Split(ctx.Param("path"), ".")[0]
		if err := bud02.DeleteBlob(
			ctx.Request.Context(),
			services,
			ctx.GetString("pk"),
			hash,
			ctx.GetString("x"),
		); err != nil {
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: err.Error()})
			return
		}

		ctx.Status(http.StatusOK)
	}
}

func listAllBlobs(queries *db.Queries, cdnBaseUrl string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limitStr := ctx.DefaultQuery("limit", "10")
		offsetStr := ctx.DefaultQuery("offset", "0")

		limit, err := strconv.ParseInt(limitStr, 10, 64)
		if err != nil || limit <= 0 {
			limit = 10
		}

		offset, err := strconv.ParseInt(offsetStr, 10, 64)
		if err != nil || offset < 0 {
			offset = 0
		}

		result, err := bud02.ListAllBlobs(ctx.Request.Context(), queries, cdnBaseUrl, limit, offset)
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusInternalServerError, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, gin.H{
			"blobs": fromSliceDomainBlobDescriptor(result.Blobs),
			"total": result.Total,
		})
	}
}
