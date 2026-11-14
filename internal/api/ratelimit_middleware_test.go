package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/0ceanSlim/lotus/internal/ratelimit"
)

const (
	testPubkeyA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testPubkeyB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testPubkeyC = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testPubkeyD = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

// limitedRouter mounts the limiter middleware behind a stand-in for
// nostrAuthMiddleware that takes the pubkey from X-Test-Pubkey.
func limitedRouter(t *testing.T, cfg ratelimit.Config, trusted trustedPubkeyFunc) (*gin.Engine, *ratelimit.Limiter) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	l, err := ratelimit.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	setPubkey := func(c *gin.Context) {
		c.Set("pk", c.GetHeader("X-Test-Pubkey"))
		c.Next()
	}
	r := gin.New()
	r.PUT("/upload", setPubkey, uploadLimit(l, trusted), func(c *gin.Context) {
		n, _ := io.Copy(io.Discard, c.Request.Body)
		c.JSON(http.StatusOK, gin.H{"read": n})
	})
	r.HEAD("/upload", setPubkey, uploadPeek(l, trusted), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.PUT("/mirror", setPubkey, uploadLimit(l, trusted), func(c *gin.Context) {
		c.Set(ctxUploadBytes, int64(70))
		c.Status(http.StatusOK)
	})
	return r, l
}

type reqOpt func(*http.Request)

func withPubkey(pk string) reqOpt   { return func(r *http.Request) { r.Header.Set("X-Test-Pubkey", pk) } }
func withRemote(addr string) reqOpt { return func(r *http.Request) { r.RemoteAddr = addr } }
func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }
func withUnknownLength() reqOpt     { return func(r *http.Request) { r.ContentLength = -1 } }

func do(r *gin.Engine, method, path, body string, opts ...reqOpt) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-Test-Pubkey", testPubkeyA)
	for _, o := range opts {
		o(req)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestUploadLimitRefusesWith429AndHeaders(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, UploadsPerMinute: 1}, nil)
	if w := do(r, http.MethodPut, "/upload", "hello"); w.Code != http.StatusOK {
		t.Fatalf("first upload: %d %s", w.Code, w.Body)
	}
	w := do(r, http.MethodPut, "/upload", "hello")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second upload: %d, want 429", w.Code)
	}
	if got := w.Header().Get("Retry-After"); got != "60" {
		t.Fatalf("Retry-After %q, want 60", got)
	}
	reason := w.Header().Get(HeaderXReason)
	if !strings.Contains(reason, "1 per minute") {
		t.Fatalf("X-Reason %q", reason)
	}
	var body apiError
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Message != reason {
		t.Fatalf("body %s should carry the reason %q", w.Body, reason)
	}
}

func TestUploadLimitChargesBodyWhenLengthUnknown(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, BytesPerWindow: 100, Window: time.Hour}, nil)
	if w := do(r, http.MethodPut, "/upload", strings.Repeat("x", 70), withUnknownLength()); w.Code != http.StatusOK {
		t.Fatalf("chunked upload: %d %s", w.Code, w.Body)
	}
	w := do(r, http.MethodPut, "/upload", strings.Repeat("x", 50))
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Header().Get(HeaderXReason), "used up") {
		t.Fatalf("after 70 counted bytes a 50-byte upload should be refused: %d %q", w.Code, w.Header().Get(HeaderXReason))
	}
	if w := do(r, http.MethodPut, "/upload", strings.Repeat("x", 30)); w.Code != http.StatusOK {
		t.Fatalf("30 bytes should still fit: %d", w.Code)
	}
}

func TestUploadLimitRefusesOversizeBeforeReading(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, BytesPerWindow: 100, Window: time.Hour}, nil)
	w := do(r, http.MethodPut, "/upload", strings.Repeat("x", 150))
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Header().Get(HeaderXReason), "larger than") {
		t.Fatalf("got %d %q", w.Code, w.Header().Get(HeaderXReason))
	}
	if w.Header().Get("Retry-After") != "3600" {
		t.Fatalf("Retry-After %q, want the window", w.Header().Get("Retry-After"))
	}
}

func TestMirrorIsChargedTheBlobSize(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, BytesPerWindow: 100, Window: time.Hour}, nil)
	if w := do(r, http.MethodPut, "/mirror", `{"url":"https://example.com/x"}`); w.Code != http.StatusOK {
		t.Fatalf("mirror: %d", w.Code)
	}
	if w := do(r, http.MethodPut, "/upload", strings.Repeat("x", 50)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("the mirrored 70 bytes were not charged: %d", w.Code)
	}
}

func TestForwardedAddressOnlyFromLoopback(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, UploadsPerMinute: 1}, nil)
	// Through the tunnel: the forwarded address is the bucket.
	tunnel := withRemote("127.0.0.1:40000")
	if w := do(r, http.MethodPut, "/upload", "a", tunnel, withHeader("CF-Connecting-IP", "203.0.113.1")); w.Code != http.StatusOK {
		t.Fatalf("first client: %d", w.Code)
	}
	if w := do(r, http.MethodPut, "/upload", "a", tunnel, withHeader("CF-Connecting-IP", "203.0.113.2"), withPubkey(testPubkeyB)); w.Code != http.StatusOK {
		t.Fatalf("second client has its own bucket: %d", w.Code)
	}
	if w := do(r, http.MethodPut, "/upload", "a", tunnel, withHeader("CF-Connecting-IP", "203.0.113.1")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("first client again should be refused: %d", w.Code)
	}
	// Direct peers cannot pick their bucket with the header. Fresh pubkeys,
	// since A and B are bound to the tunnel clients' addresses above.
	direct := withRemote("198.51.100.9:1234")
	if w := do(r, http.MethodPut, "/upload", "a", direct, withHeader("CF-Connecting-IP", "203.0.113.7"), withPubkey(testPubkeyC)); w.Code != http.StatusOK {
		t.Fatalf("direct peer: %d %s", w.Code, w.Header().Get(HeaderXReason))
	}
	if w := do(r, http.MethodPut, "/upload", "a", direct, withHeader("CF-Connecting-IP", "203.0.113.8"), withPubkey(testPubkeyD)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("direct peer changing the header must stay in its own bucket: %d", w.Code)
	}
}

func TestHeadUploadReportsWithoutCharging(t *testing.T) {
	r, l := limitedRouter(t, ratelimit.Config{Enabled: true, UploadsPerMinute: 1, BytesPerWindow: 100, Window: time.Hour}, nil)
	for i := range 3 {
		if w := do(r, http.MethodHead, "/upload", "", withHeader(HeaderXContentLength, "40")); w.Code != http.StatusOK {
			t.Fatalf("HEAD %d: %d", i, w.Code)
		}
	}
	if _, bindings := l.Size(); bindings != 0 {
		t.Fatal("HEAD bound the pubkey")
	}
	w := do(r, http.MethodHead, "/upload", "", withHeader(HeaderXContentLength, "400"))
	if w.Code != http.StatusTooManyRequests || !strings.Contains(w.Header().Get(HeaderXUploadMessage), "larger than") {
		t.Fatalf("oversize HEAD: %d %q", w.Code, w.Header().Get(HeaderXUploadMessage))
	}
	if w.Body.Len() != 0 {
		t.Fatal("HEAD must not carry a body")
	}
	if w := do(r, http.MethodPut, "/upload", "payload"); w.Code != http.StatusOK {
		t.Fatalf("PUT after HEADs: %d", w.Code)
	}
	w = do(r, http.MethodHead, "/upload", "", withHeader(HeaderXContentLength, "40"))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" || !strings.Contains(w.Header().Get(HeaderXReason), "per minute") {
		t.Fatalf("HEAD should report the drained bucket: %d %q", w.Code, w.Header().Get(HeaderXReason))
	}
}

func TestTrustedPubkeyExemption(t *testing.T) {
	trusted := func(pk string) bool { return pk == testPubkeyA }
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, UploadsPerMinute: 1, ExemptTrusted: true}, trusted)
	for i := range 5 {
		if w := do(r, http.MethodPut, "/upload", "a"); w.Code != http.StatusOK {
			t.Fatalf("trusted upload %d refused: %d", i, w.Code)
		}
	}
	if w := do(r, http.MethodPut, "/upload", "a", withPubkey(testPubkeyB)); w.Code != http.StatusOK {
		t.Fatalf("stranger first upload: %d", w.Code)
	}
	if w := do(r, http.MethodPut, "/upload", "a", withPubkey(testPubkeyB)); w.Code != http.StatusTooManyRequests {
		t.Fatalf("stranger second upload should be refused: %d", w.Code)
	}

	// With the flag off, trust changes nothing.
	r, _ = limitedRouter(t, ratelimit.Config{Enabled: true, UploadsPerMinute: 1, ExemptTrusted: false}, trusted)
	do(r, http.MethodPut, "/upload", "a")
	if w := do(r, http.MethodPut, "/upload", "a"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("exemption applied without the flag: %d", w.Code)
	}
}

func TestLimiterOffIsTransparent(t *testing.T) {
	r, l := limitedRouter(t, ratelimit.Config{Enabled: false, UploadsPerMinute: 1}, nil)
	for i := range 5 {
		w := do(r, http.MethodPut, "/upload", "a")
		if w.Code != http.StatusOK || w.Header().Get("Retry-After") != "" || w.Header().Get(HeaderXReason) != "" {
			t.Fatalf("upload %d touched by a disabled limiter: %d %v", i, w.Code, w.Header())
		}
	}
	if w := do(r, http.MethodHead, "/upload", "", withHeader(HeaderXContentLength, "1")); w.Code != http.StatusOK {
		t.Fatalf("HEAD: %d", w.Code)
	}
	if a, b := l.Size(); a != 0 || b != 0 {
		t.Fatal("a disabled limiter recorded something")
	}
	// Hot enable takes effect on the next request.
	if err := l.Reload(ratelimit.Config{Enabled: true, UploadsPerMinute: 1}); err != nil {
		t.Fatal(err)
	}
	do(r, http.MethodPut, "/upload", "a")
	if w := do(r, http.MethodPut, "/upload", "a"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("limiter did not take effect after reload: %d", w.Code)
	}
}

func TestConcurrencySlotIsReleasedAfterHandler(t *testing.T) {
	r, _ := limitedRouter(t, ratelimit.Config{Enabled: true, MaxConcurrent: 1}, nil)
	for i := range 4 {
		if w := do(r, http.MethodPut, "/upload", "a"); w.Code != http.StatusOK {
			t.Fatalf("sequential upload %d refused: %d %s", i, w.Code, w.Header().Get(HeaderXReason))
		}
	}
}

func TestNilLimiterIsTransparent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.PUT("/upload", uploadLimit(nil, nil), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.HEAD("/upload", uploadPeek(nil, nil), func(c *gin.Context) { c.Status(http.StatusOK) })
	if w := do(r, http.MethodPut, "/upload", "a"); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if w := do(r, http.MethodHead, "/upload", ""); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
}
