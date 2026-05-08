package api

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/bud06"
	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/hashing"
)

func uploadRequirements(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		blobHash := ctx.GetHeader(HeaderXSHA256)
		if err := hashing.IsSHA256(blobHash); err != nil {
			ctx.Header(HeaderXUploadMessage, fmt.Sprintf("invalid SHA-256: %s", err))
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}

		contentType := ctx.GetHeader(HeaderXContentType)
		contentLength, err := strconv.Atoi(ctx.GetHeader(HeaderXContentLength))
		if err != nil {
			ctx.Header(HeaderXUploadMessage, "couldn't parse Content-Length as an integer")
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}

		authedPubkey := ctx.GetString("pk")
		if err := bud06.UploadRequirements(ctx, services, authedPubkey, blobHash, contentType, contentLength); err != nil {
			ctx.Header(HeaderXUploadMessage, err.Error())
			ctx.AbortWithStatus(http.StatusBadRequest)
			return
		}

		ctx.Status(http.StatusOK)
	}
}
