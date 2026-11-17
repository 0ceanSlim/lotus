package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/hashing"
	"github.com/0ceanSlim/lotus/internal/retention"
)

const (
	HeaderXExpiresAt = "X-Expires-At"
	HeaderXRetention = "X-Retention"
	ctxRetention     = "retention"
)

// retentionOf returns the expiry controller when the services have one.
func retentionOf(services core.Services) *retention.Controller {
	if rs, ok := services.(interface{ Retention() *retention.Controller }); ok {
		return rs.Retention()
	}
	return nil
}

// mountRetention adds the public storage endpoints. They read only
// aggregates and hand out quotes; nothing about the caller is kept.
//
//	GET /api/v1/storage                       live state, curve and history
//	GET /api/v1/storage/quote?sha256=&size=   a held expiry for one file
func mountRetention(r *gin.Engine, ctl *retention.Controller) {
	r.Use(func(ctx *gin.Context) { ctx.Set(ctxRetention, ctl) })

	r.GET("/api/v1/storage", func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		ctx.JSON(http.StatusOK, ctl.State(240))
	})

	r.GET("/api/v1/storage/quote", func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "no-store")
		sha := strings.ToLower(ctx.Query("sha256"))
		if err := hashing.IsSHA256(sha); err != nil {
			refuse(ctx, http.StatusBadRequest, "sha256 must be a 64-character hex hash")
			return
		}
		size, err := strconv.ParseInt(ctx.Query("size"), 10, 64)
		if err != nil || size <= 0 {
			refuse(ctx, http.StatusBadRequest, "size must be a positive number of bytes")
			return
		}
		st := ctl.State(0)
		if size > st.SizeMax {
			refuse(ctx, http.StatusRequestEntityTooLarge, "file is larger than "+strconv.FormatInt(st.SizeMax, 10)+" bytes")
			return
		}
		if err := ctl.Admit(size); err != nil {
			ctx.Header("Retry-After", retryAfterSeconds(ctl))
			refuse(ctx, http.StatusInsufficientStorage, "the server is full right now")
			return
		}
		ctx.JSON(http.StatusOK, ctl.Quote(sha, size, time.Now()))
	})
}

// uploadQuoteHeaders adds the held expiry to a HEAD /upload answer (BUD-06),
// so any Blossom client can show it before sending the file.
func uploadQuoteHeaders(services core.Services) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctl := retentionOf(services)
		if ctl == nil {
			return
		}
		sha := strings.ToLower(ctx.GetHeader(HeaderXSHA256))
		size, err := strconv.ParseInt(ctx.GetHeader(HeaderXContentLength), 10, 64)
		if hashing.IsSHA256(sha) != nil || err != nil || size <= 0 {
			return
		}
		if ctl.Admit(size) != nil {
			return
		}
		q := ctl.Quote(sha, size, time.Now())
		ctx.Header(HeaderXExpiresAt, strconv.FormatInt(q.ExpiresAt, 10))
		ctx.Header(HeaderXRetention, strconv.FormatInt(q.Retention, 10))
	}
}

func retryAfterSeconds(ctl *retention.Controller) string {
	return strconv.FormatInt(int64(ctl.RetryAfter(time.Now()).Seconds()), 10)
}
