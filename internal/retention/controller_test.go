package retention

import (
	"context"
	"math"
	"testing"
	"time"
)

const (
	mb  = 1 << 20
	gb  = 1 << 30
	day = 24 * time.Hour
)

type fakeSource struct {
	items  []Commitment
	recent []int64
}

func (f *fakeSource) Commitments(_ context.Context, now int64) ([]Commitment, error) {
	var out []Commitment
	for _, it := range f.items {
		if it.Expiry == 0 || it.Expiry > now {
			out = append(out, it)
		}
	}
	return out, nil
}

func (f *fakeSource) RecentSizes(context.Context, int64) ([]int64, error) { return f.recent, nil }

func params() Params {
	return Params{
		CapacityBytes: 100 * gb,
		TargetFill:    0.7,
		HardFill:      0.95,
		TTLMin:        day,
		TTLMax:        365 * day,
		Alpha:         0.5,
		SizeRefBytes:  mb,
		SizeMaxBytes:  50 * mb,
		QuoteHold:     15 * time.Minute,
	}
}

func seeded(t *testing.T, src *fakeSource, now time.Time) *Controller {
	t.Helper()
	c := New(params(), src)
	if err := c.Seed(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestQuietStoreGivesMaximum(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := seeded(t, &fakeSource{}, now)
	for _, s := range []int64{1024, mb, 50 * mb} {
		if got := c.Lifetime(s); got != 365*day {
			t.Errorf("size %d: lifetime %v, want the maximum", s, got)
		}
	}
}

func TestFullStoreGivesMinimum(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	// 80 GB held for another 100 days: already past the 70% target.
	src := &fakeSource{items: []Commitment{{Size: 80 * gb, Expiry: now.Add(100 * day).Unix()}}, recent: []int64{mb}}
	c := seeded(t, src, now)
	if got := c.Lifetime(mb); got != day {
		t.Errorf("lifetime %v, want the minimum while over target", got)
	}
}

func TestMoreTrafficShorterLifetimes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	light := make([]int64, 7*100) // 100 MB a day
	for i := range light {
		light[i] = mb
	}
	heavy := make([]int64, 7*10000) // 10 GB a day
	for i := range heavy {
		heavy[i] = mb
	}
	lc := seeded(t, &fakeSource{recent: light}, now)
	hc := seeded(t, &fakeSource{recent: heavy}, now)
	l, h := lc.Lifetime(mb), hc.Lifetime(mb)
	if !(h < l) {
		t.Fatalf("heavy traffic lifetime %v should be shorter than light %v", h, l)
	}
	// Little's law: 10 GB/day held ~ lifetime must keep it near 70 GB.
	if h > 8*day || h < 5*day {
		t.Errorf("heavy lifetime %v, want about a week (70 GB / 10 GB a day)", h)
	}
}

func TestBiggerFilesLiveShorter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	sizes := make([]int64, 7*3000)
	for i := range sizes {
		sizes[i] = mb
	}
	c := seeded(t, &fakeSource{recent: sizes}, now)
	if !(c.Lifetime(40*mb) < c.Lifetime(mb) && c.Lifetime(mb) < c.Lifetime(10*1024)) {
		t.Errorf("lifetimes should fall with size: 10K %v, 1M %v, 40M %v", c.Lifetime(10*1024), c.Lifetime(mb), c.Lifetime(40*mb))
	}
}

func TestQuoteIsHeldAndConsumed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := seeded(t, &fakeSource{}, now)
	q := c.Quote("abc", mb, now)
	if q.ExpiresAt != now.Add(365*day).Unix() {
		t.Fatalf("quote %d", q.ExpiresAt)
	}
	// Repeating the quote within the hold returns the same one.
	if q2 := c.Quote("abc", mb, now.Add(time.Minute)); q2 != q {
		t.Errorf("requote changed: %+v vs %+v", q2, q)
	}
	// The store gets busier, but the held quote is honoured.
	c.mu.Lock()
	c.theta = c.p.thetaLo()
	c.mu.Unlock()
	if got := c.ExpiryFor("abc", mb, now.Add(2*time.Minute)); got != q.ExpiresAt {
		t.Errorf("expiry %d, want held quote %d", got, q.ExpiresAt)
	}
	// Consumed: a second store gets the current (short) lifetime.
	if got := c.ExpiryFor("abc", mb, now.Add(3*time.Minute)); got != now.Add(3*time.Minute+day).Unix() {
		t.Errorf("after use: %d", got)
	}
}

func TestExpiredOrMismatchedQuoteIsNotHonoured(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	c := seeded(t, &fakeSource{}, now)
	q := c.Quote("abc", mb, now)
	c.mu.Lock()
	c.theta = c.p.thetaLo()
	c.mu.Unlock()
	if got := c.ExpiryFor("abc", 2*mb, now.Add(time.Minute)); got == q.ExpiresAt {
		t.Error("a quote for another size was honoured")
	}
	c.Quote("def", mb, now)
	if got := c.ExpiryFor("def", mb, now.Add(16*time.Minute)); got == q.ExpiresAt {
		t.Error("an expired hold was honoured")
	}
}

func TestAdmitRefusesPastHardFill(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	src := &fakeSource{items: []Commitment{{Size: 94 * gb, Expiry: now.Add(3 * day).Unix()}}}
	c := seeded(t, src, now)
	if err := c.Admit(500 * mb); err != nil {
		t.Errorf("under the hard fill: %v", err)
	}
	if err := c.Admit(2 * gb); err != ErrFull {
		t.Errorf("past the hard fill: %v", err)
	}
	if ra := c.RetryAfter(now); ra != 3*day {
		t.Errorf("retry after %v, want the next expiry", ra)
	}
}

func TestThetaRisesSlowly(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	src := &fakeSource{items: []Commitment{{Size: 80 * gb, Expiry: now.Add(2 * time.Hour).Unix()}}}
	c := seeded(t, src, now)
	before := c.State(0).Theta
	// The big commitment expires; with no traffic the store could give the
	// maximum at once, but theta may only double per day.
	if err := c.Tick(context.Background(), now.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	after := c.State(0).Theta
	if want := before * math.Pow(2, 3.0/24) * (1 + 1e-9); after <= before || after > want {
		t.Errorf("theta went %v -> %v in 3h; want at most %v", before, after, want)
	}
}
