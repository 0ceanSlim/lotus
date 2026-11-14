package ratelimit

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	addrA = "203.0.113.5"
	addrB = "198.51.100.7"
	keyP  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keyQ  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// newTest returns a limiter on a fake clock; advance moves the clock.
func newTest(t *testing.T, cfg Config) (*Limiter, func(time.Duration)) {
	t.Helper()
	l, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return clock }
	return l, func(d time.Duration) { clock = clock.Add(d) }
}

func mustAllow(t *testing.T, l *Limiter, addr, pubkey string, size int64) *Reservation {
	t.Helper()
	r, d := l.Reserve(addr, pubkey, size)
	if d != nil {
		t.Fatalf("expected %s/%s size %d to be allowed, got denial %q retry %s", addr, pubkey[:4], size, d.Reason, d.RetryAfter)
	}
	return r
}

func mustDeny(t *testing.T, l *Limiter, addr, pubkey string, size int64, reasonPart string) *Denial {
	t.Helper()
	r, d := l.Reserve(addr, pubkey, size)
	if d == nil {
		t.Fatalf("expected %s/%s size %d to be denied", addr, pubkey[:4], size)
	}
	if r != nil {
		t.Fatal("a denied request must not return a reservation")
	}
	if !strings.Contains(d.Reason, reasonPart) {
		t.Fatalf("denial reason %q does not mention %q", d.Reason, reasonPart)
	}
	return d
}

func TestUploadBucketLimitsAndRefills(t *testing.T) {
	l, advance := newTest(t, Config{Enabled: true, UploadsPerMinute: 3})
	for range 3 {
		mustAllow(t, l, addrA, keyP, 10).Release()
	}
	d := mustDeny(t, l, addrA, keyP, 10, "3 per minute")
	// One token every 20 seconds.
	if d.RetryAfter < 19*time.Second || d.RetryAfter > 20*time.Second {
		t.Fatalf("retry after %s, want about 20s", d.RetryAfter)
	}
	if d.RetryAfterSeconds() != 20 {
		t.Fatalf("Retry-After header %d, want 20", d.RetryAfterSeconds())
	}
	advance(20 * time.Second)
	mustAllow(t, l, addrA, keyP, 10).Release()
	mustDeny(t, l, addrA, keyP, 10, "per minute")
	// Another address is unaffected.
	mustAllow(t, l, addrB, keyQ, 10).Release()
}

func TestByteBudgetRefillsOverWindow(t *testing.T) {
	// 100 bytes per 100 seconds: one byte a second.
	l, advance := newTest(t, Config{Enabled: true, BytesPerWindow: 100, Window: 100 * time.Second})
	mustAllow(t, l, addrA, keyP, 60).Release()
	d := mustDeny(t, l, addrA, keyP, 50, "used up")
	if d.RetryAfter < 9*time.Second || d.RetryAfter > 10*time.Second {
		t.Fatalf("retry after %s, want about 10s", d.RetryAfter)
	}
	advance(10 * time.Second)
	mustAllow(t, l, addrA, keyP, 50).Release()

	// A file bigger than the whole budget can never fit.
	d = mustDeny(t, l, addrB, keyQ, 200, "larger than")
	if d.RetryAfter != 100*time.Second {
		t.Fatalf("retry after %s, want the window", d.RetryAfter)
	}
}

func TestUnknownSizeIsSettledAfterwards(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, BytesPerWindow: 100, Window: time.Hour})
	r := mustAllow(t, l, addrA, keyP, -1)
	r.Settle(70)
	r.Release()
	mustDeny(t, l, addrA, keyP, 50, "used up")
	mustAllow(t, l, addrA, keyP, 30).Release()
}

func TestSettleChargesOnlyTheExcess(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, BytesPerWindow: 100, Window: time.Hour})
	r := mustAllow(t, l, addrA, keyP, 40)
	r.Settle(30) // fewer bytes than declared: no refund
	r.Settle(60) // more than declared: charge the extra 20
	r.Release()
	mustDeny(t, l, addrA, keyP, 41, "used up")
	mustAllow(t, l, addrA, keyP, 40).Release()
}

func TestConcurrencyCap(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, MaxConcurrent: 1})
	first := mustAllow(t, l, addrA, keyP, 10)
	d := mustDeny(t, l, addrA, keyQ, 10, "in progress")
	if d.RetryAfterSeconds() < 1 {
		t.Fatal("concurrency denial needs a positive Retry-After")
	}
	first.Release()
	first.Release() // idempotent
	mustAllow(t, l, addrA, keyQ, 10).Release()
}

func TestPubkeyBoundToFirstAddressForOneWindow(t *testing.T) {
	window := time.Hour
	l, advance := newTest(t, Config{Enabled: true, UploadsPerMinute: 2, Window: window})

	// P exhausts A's budget.
	mustAllow(t, l, addrA, keyP, 1).Release()
	mustAllow(t, l, addrA, keyP, 1).Release()

	// Switching address does not help P: A is still charged.
	mustDeny(t, l, addrB, keyP, 1, "per minute")
	// B itself is fine for a different key.
	mustAllow(t, l, addrB, keyQ, 1).Release()

	// Once both have refilled, P from B charges B as well as A: two uploads
	// drain both, so Q (bound to B) is refused at B and P is refused at A.
	advance(time.Minute)
	mustAllow(t, l, addrB, keyP, 1).Release()
	mustAllow(t, l, addrB, keyP, 1).Release()
	mustDeny(t, l, addrB, keyQ, 1, "per minute")
	mustDeny(t, l, addrA, keyP, 1, "per minute")

	// After the window the bindings are gone: P is fresh at B and no longer
	// touches A.
	advance(window + time.Second)
	mustAllow(t, l, addrB, keyP, 1).Release()
	mustAllow(t, l, addrB, keyP, 1).Release()
	mustAllow(t, l, addrA, keyQ, 1).Release() // A is untouched by P now
}

func TestTablesHoldOnlyKeyedHashes(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, UploadsPerMinute: 5})
	mustAllow(t, l, addrA, keyP, 1).Release()
	plain := sha256.Sum256([]byte(addrA))
	for k := range l.addrs {
		if k == addrA || k == hex.EncodeToString(plain[:]) || string(plain[:16]) == k {
			t.Fatal("address table key is derivable without the secret")
		}
		if len(k) != 16 {
			t.Fatalf("table key length %d, want 16-byte HMAC prefix", len(k))
		}
	}
	for k := range l.keys {
		if k == keyP || strings.Contains(k, keyP[:8]) {
			t.Fatal("pubkey table key is the pubkey")
		}
	}
	other, _ := newTest(t, Config{Enabled: true, UploadsPerMinute: 5})
	if l.hash("addr", addrA) == other.hash("addr", addrA) {
		t.Fatal("two limiters hash an address the same way: the secret is not random")
	}
}

func TestPeekConsumesNothing(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, UploadsPerMinute: 1, BytesPerWindow: 100, Window: time.Hour, MaxConcurrent: 1})
	for i := range 5 {
		if d := l.Peek(addrA, keyP, 50); d != nil {
			t.Fatalf("peek %d denied: %s", i, d.Reason)
		}
	}
	if _, bindings := l.Size(); bindings != 0 {
		t.Fatal("peek must not bind the pubkey")
	}
	if d := l.Peek(addrA, keyP, 200); d == nil || !strings.Contains(d.Reason, "larger") {
		t.Fatal("peek should report an oversize file")
	}
	r := mustAllow(t, l, addrA, keyP, 50)
	if d := l.Peek(addrA, keyQ, 1); d == nil || !strings.Contains(d.Reason, "in progress") {
		t.Fatal("peek should report the concurrency cap")
	}
	r.Release()
	if d := l.Peek(addrA, keyQ, 1); d == nil || !strings.Contains(d.Reason, "per minute") {
		t.Fatal("peek should report the drained upload bucket")
	}
	// P is bound to A; peeking from B consults A's bucket too.
	if d := l.Peek(addrB, keyP, 1); d == nil {
		t.Fatal("peek should consult the bound address")
	}
}

func TestSweepDropsIdleEntries(t *testing.T) {
	window := 10 * time.Minute
	l, advance := newTest(t, Config{Enabled: true, UploadsPerMinute: 5, Window: window})
	mustAllow(t, l, addrA, keyP, 1).Release()
	if a, b := l.Size(); a != 1 || b != 1 {
		t.Fatalf("sizes %d/%d, want 1/1", a, b)
	}
	advance(window + time.Minute)
	mustAllow(t, l, addrB, keyQ, 1).Release() // triggers the sweep
	if a, b := l.Size(); a != 1 || b != 1 {
		t.Fatalf("after a window: sizes %d/%d, want only the new entries (1/1)", a, b)
	}
	if _, ok := l.addrs[l.hash("addr", addrA)]; ok {
		t.Fatal("idle address survived the sweep")
	}
}

func TestInFlightEntriesSurviveSweep(t *testing.T) {
	window := 10 * time.Minute
	l, advance := newTest(t, Config{Enabled: true, MaxConcurrent: 2, Window: window})
	r := mustAllow(t, l, addrA, keyP, 1)
	advance(window + time.Minute)
	mustAllow(t, l, addrB, keyQ, 1).Release()
	if _, ok := l.addrs[l.hash("addr", addrA)]; !ok {
		t.Fatal("address with an upload in flight was swept")
	}
	r.Release()
}

func TestDisabledAllowsEverything(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: false, UploadsPerMinute: 1})
	for range 10 {
		r := mustAllow(t, l, addrA, keyP, 1<<40)
		r.Settle(1 << 41)
		r.Release()
	}
	if a, b := l.Size(); a != 0 || b != 0 {
		t.Fatal("a disabled limiter must not record anything")
	}
}

func TestReloadChangesLimitsAndKeepsSecret(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, UploadsPerMinute: 1})
	before := l.hash("addr", addrA)
	mustAllow(t, l, addrA, keyP, 1).Release()
	mustDeny(t, l, addrA, keyP, 1, "1 per minute")
	if err := l.Reload(Config{Enabled: true, UploadsPerMinute: 3}); err != nil {
		t.Fatal(err)
	}
	if l.hash("addr", addrA) != before {
		t.Fatal("reload changed the secret")
	}
	// The drained bucket refills towards the new capacity rather than jumping.
	mustDeny(t, l, addrA, keyP, 1, "3 per minute")
	if err := l.Reload(Config{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	mustAllow(t, l, addrA, keyP, 1).Release()
	if err := l.Reload(Config{Enabled: true, TrustedProxies: []string{"not an ip"}}); err == nil {
		t.Fatal("expected a bad trusted proxy to be rejected")
	}
}

func TestClientIPTrustRule(t *testing.T) {
	l, _ := newTest(t, Config{Enabled: true, TrustedProxies: []string{"10.0.0.0/8", "192.0.2.1"}})
	cases := []struct {
		name   string
		remote string
		header map[string]string
		want   string
	}{
		{"direct peer, header ignored", "203.0.113.9:1234", map[string]string{"CF-Connecting-IP": "198.51.100.1"}, "203.0.113.9"},
		{"loopback tunnel, CF header", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "198.51.100.1"}, "198.51.100.1"},
		{"loopback v6 tunnel, X-Real-IP", "[::1]:5000", map[string]string{"X-Real-IP": "198.51.100.2"}, "198.51.100.2"},
		{"CF header wins over X-Real-IP", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "198.51.100.1", "X-Real-IP": "198.51.100.2"}, "198.51.100.1"},
		{"trusted CIDR proxy", "10.1.2.3:80", map[string]string{"CF-Connecting-IP": "198.51.100.3"}, "198.51.100.3"},
		{"trusted single proxy", "192.0.2.1:80", map[string]string{"CF-Connecting-IP": "198.51.100.4"}, "198.51.100.4"},
		{"untrusted peer in a nearby range", "192.0.2.2:80", map[string]string{"CF-Connecting-IP": "198.51.100.4"}, "192.0.2.2"},
		{"garbage header falls back to peer", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "not-an-ip"}, "127.0.0.1"},
		{"no header from loopback", "127.0.0.1:5000", nil, "127.0.0.1"},
		{"forwarded IPv6 bucketed by /64", "127.0.0.1:5000", map[string]string{"CF-Connecting-IP": "2001:db8:1:2:3:4:5:6"}, "2001:db8:1:2::/64"},
		{"direct IPv6 bucketed by /64", "[2001:db8:aa:bb:cc:dd:ee:ff]:443", nil, "2001:db8:aa:bb::/64"},
		{"peer without port", "203.0.113.10", nil, "203.0.113.10"},
		{"forwarded IPv4-mapped v6", "127.0.0.1:1", map[string]string{"CF-Connecting-IP": "::ffff:198.51.100.9"}, "198.51.100.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/upload", nil)
			req.RemoteAddr = tc.remote
			for k, v := range tc.header {
				req.Header.Set(k, v)
			}
			if got := l.ClientIP(req); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHumanFormats(t *testing.T) {
	bytes := map[int64]string{
		52428800:   "50 MB",
		524288000:  "500 MB",
		1073741824: "1 GB",
		1610612736: "1.5 GB",
		1536:       "1.5 KB",
		12:         "12 bytes",
	}
	for n, want := range bytes {
		if got := HumanBytes(n); got != want {
			t.Errorf("HumanBytes(%d) = %q, want %q", n, got, want)
		}
	}
	durations := map[time.Duration]string{
		24 * time.Hour:   "day",
		48 * time.Hour:   "2 days",
		time.Hour:        "hour",
		12 * time.Hour:   "12 hours",
		90 * time.Minute: "90 minutes",
		time.Minute:      "minute",
		90 * time.Second: "1m30s",
	}
	for d, want := range durations {
		if got := HumanDuration(d); got != want {
			t.Errorf("HumanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
