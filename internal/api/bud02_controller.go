package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/bud02"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/db"
)

func uploadBlob(services core.Services, cdnBaseUrl string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		bodyBytes, err := io.ReadAll(ctx.Request.Body)
		defer ctx.Request.Body.Close()
		if err != nil {
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
			ctx.AbortWithStatusJSON(http.StatusBadRequest, apiError{Message: err.Error()})
			return
		}

		ctx.JSON(http.StatusOK, fromDomainBlobDescriptor(blobDescriptor))
	}
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
		if err := bud02.DeleteBlob(
			ctx.Request.Context(),
			services,
			ctx.GetString("pk"),
			ctx.Param("path"),
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
