package api

import (
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/core"
	"github.com/0ceanSlim/lotus/internal/ratelimit"
)

// ctxUploadBytes is set by a handler whose request body does not carry the
// stored bytes (mirror fetches them), so the limiter can charge the right
// amount after the handler ran.
const ctxUploadBytes = "upload_bytes"

// trustedPubkeyFunc reports whether the operator vouches for a pubkey.
type trustedPubkeyFunc func(pubkey string) bool

// trustedPubkeys adapts the access-rule service, which knows the explicit
// rules and the nostr.json members, for the limiter's optional exemption.
func trustedPubkeys(services core.Services) trustedPubkeyFunc {
	t, ok := services.ACR().(interface{ IsTrusted(string) bool })
	if !ok {
		return func(string) bool { return false }
	}
	return t.IsTrusted
}

// uploadLimit charges an upload to the network it came from, after
// nostrAuthMiddleware has put the signer's pubkey on the context. A refused
// request gets 429 with Retry-After and X-Reason. The body is counted as it
// is read so an upload without a Content-Length is charged afterwards.
func uploadLimit(l *ratelimit.Limiter, trusted trustedPubkeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil {
			c.Next()
			return
		}
		cfg := l.Config()
		if !cfg.Enabled {
			c.Next()
			return
		}
		pubkey := c.GetString("pk")
		if cfg.ExemptTrusted && trusted != nil && trusted(pubkey) {
			c.Next()
			return
		}

		res, denied := l.Reserve(l.ClientIP(c.Request), pubkey, c.Request.ContentLength)
		if denied != nil {
			writeDenial(c, denied)
			return
		}
		defer res.Release()

		var counted *countingBody
		if c.Request.Body != nil {
			counted = &countingBody{ReadCloser: c.Request.Body}
			c.Request.Body = counted
		}
		c.Next()

		if v, ok := c.Get(ctxUploadBytes); ok {
			if size, ok := v.(int64); ok {
				res.Settle(size)
				return
			}
		}
		if counted != nil {
			res.Settle(counted.n)
		}
	}
}

// uploadPeek answers HEAD /upload for the limiter: a would-be refusal is
// reported with the same headers PUT would get, plus X-Upload-Message for
// BUD-06 clients, and nothing is charged or bound.
func uploadPeek(l *ratelimit.Limiter, trusted trustedPubkeyFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if l == nil {
			c.Next()
			return
		}
		cfg := l.Config()
		if !cfg.Enabled {
			c.Next()
			return
		}
		pubkey := c.GetString("pk")
		if cfg.ExemptTrusted && trusted != nil && trusted(pubkey) {
			c.Next()
			return
		}
		size := int64(-1)
		if v, err := strconv.ParseInt(c.GetHeader(HeaderXContentLength), 10, 64); err == nil {
			size = v
		}
		if denied := l.Peek(l.ClientIP(c.Request), pubkey, size); denied != nil {
			c.Header(HeaderXUploadMessage, denied.Reason)
			writeDenial(c, denied)
			return
		}
		c.Next()
	}
}

// chargeOnce counts one upload-sized action by pubkey from the request's
// network, with no bytes and no in-flight slot, for lotus endpoints that
// create rows on a signature alone (short links). It writes the 429 itself
// and reports whether the caller may go on.
func chargeOnce(c *gin.Context, l *ratelimit.Limiter, trusted trustedPubkeyFunc, pubkey string) bool {
	if l == nil {
		return true
	}
	cfg := l.Config()
	if !cfg.Enabled {
		return true
	}
	if cfg.ExemptTrusted && trusted != nil && trusted(pubkey) {
		return true
	}
	res, denied := l.Reserve(l.ClientIP(c.Request), pubkey, 0)
	if denied != nil {
		writeDenial(c, denied)
		return false
	}
	res.Release()
	return true
}

func writeDenial(c *gin.Context, d *ratelimit.Denial) {
	c.Header("Retry-After", strconv.Itoa(d.RetryAfterSeconds()))
	c.Header(HeaderXReason, d.Reason)
	if c.Request.Method == http.MethodHead {
		c.AbortWithStatus(http.StatusTooManyRequests)
		return
	}
	c.AbortWithStatusJSON(http.StatusTooManyRequests, apiError{Message: d.Reason})
}

// countingBody counts the bytes a handler reads from the request body.
type countingBody struct {
	io.ReadCloser
	n int64
}

func (b *countingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.n += int64(n)
	return n, err
}
