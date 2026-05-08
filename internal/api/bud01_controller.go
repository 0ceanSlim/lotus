package api

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/bud01"
	"github.com/0ceanSlim/lotus/internal/core"
)

func getBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		blob, err := bud01.GetBlob(ctx.Request.Context(), services, pathParts[0])
		if err != nil {
			ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: err.Error()})
			return
		}

		// 0x0 mode: blob bytes are nil, proxy content from the internal 0x0.st instance.
		if blob.Blob == nil {
			upstream, err := http.Get(blob.ExternalUrl)
			if err != nil || upstream.StatusCode != http.StatusOK {
				ctx.AbortWithStatusJSON(http.StatusNotFound, apiError{Message: "blob not available"})
				return
			}
			defer upstream.Body.Close()
			ctx.Header("Content-Type", upstream.Header.Get("Content-Type"))
			ctx.Header("Content-Length", upstream.Header.Get("Content-Length"))
			ctx.Status(http.StatusOK)
			io.Copy(ctx.Writer, upstream.Body)
			return
		}

		fileBytes := blob.Blob
		mType := mimetype.Detect(fileBytes)
		ctx.Header("Content-Type", mType.String())
		ctx.Header("Accept-Ranges", "bytes")

		rangeHeader := ctx.GetHeader("Range")
		if rangeHeader == "" {
			ctx.Header("Content-Length", strconv.FormatInt(int64(len(fileBytes)), 10))
			_, _ = ctx.Writer.Write(fileBytes)
			ctx.Status(http.StatusOK)
			return
		}

		start, end, err := parseRangeHeader(rangeHeader, int64(len(fileBytes)))
		if err != nil {
			ctx.Header("Content-Range", fmt.Sprintf("bytes */%d", len(fileBytes)))
			ctx.AbortWithStatus(http.StatusRequestedRangeNotSatisfiable)
			return
		}

		contentLength := end - start + 1
		ctx.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(fileBytes)))
		ctx.Header("Content-Length", strconv.FormatInt(contentLength, 10))
		ctx.Status(http.StatusPartialContent)
		_, _ = ctx.Writer.Write(fileBytes[start : end+1])
	}
}

func hasBlob(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		pathParts := strings.Split(ctx.Param("path"), ".")
		_, err := bud01.HasBlob(ctx.Request.Context(), services, pathParts[0])
		if err != nil {
			ctx.AbortWithStatus(http.StatusNotFound)
			return
		}
		ctx.Status(http.StatusOK)
	}
}

func parseRangeHeader(rangeHeader string, fileSize int64) (int64, int64, error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return 0, 0, fmt.Errorf("invalid range header format")
	}

	rangeSpec := strings.TrimPrefix(rangeHeader, "bytes=")
	ranges := strings.Split(rangeSpec, ",")
	if len(ranges) > 1 {
		return 0, 0, fmt.Errorf("multiple ranges not supported")
	}

	parts := strings.Split(ranges[0], "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	var start, end int64
	var err error

	if parts[0] != "" && parts[1] != "" {
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start position")
		}
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid end position")
		}
	} else if parts[0] != "" && parts[1] == "" {
		start, err = strconv.ParseInt(parts[0], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid start position")
		}
		end = fileSize - 1
	} else if parts[0] == "" && parts[1] != "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid suffix length")
		}
		start = fileSize - suffix
		if start < 0 {
			start = 0
		}
		end = fileSize - 1
	} else {
		return 0, 0, fmt.Errorf("invalid range format")
	}

	if start < 0 || end < 0 || start > end || start >= fileSize {
		return 0, 0, fmt.Errorf("range not satisfiable")
	}

	if end >= fileSize {
		end = fileSize - 1
	}

	return start, end, nil
}
