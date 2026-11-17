// Package retention picks how long each upload is kept so a fixed-size store
// holds its usage near a target by itself. It implements the look-ahead
// controller from docs/self-regulating-storage: every stored blob's expiry is
// a promise fixed at upload, so the store knows exactly how many bytes it
// will still hold at any future moment, and it chooses the lifetime scale for
// new uploads so that committed bytes plus the expected new stream stay under
// the target at every horizon.
//
// Lifetimes are T(s) = clamp(theta * (s_ref/s)^alpha, ttl_min, ttl_max).
// Only theta moves. A quote fixes a file's expiry for a short hold so the
// uploader can see it and confirm before sending the bytes.
package retention

import (
	"context"
	"errors"
	"math"
	"sort"
	"sync"
	"time"
)

// Params are the operator's bounds.
type Params struct {
	CapacityBytes int64
	TargetFill    float64
	HardFill      float64
	TTLMin        time.Duration
	TTLMax        time.Duration
	Alpha         float64
	SizeRefBytes  int64
	SizeMaxBytes  int64         // largest file accepted (max_upload_size_bytes)
	QuoteHold     time.Duration // how long a quoted expiry is held
	MaxRisePerDay float64       // theta may grow at most this factor per day
	EWMAHalfLife  time.Duration // smoothing of the inflow estimate
}

// Source is the store's view of what it holds.
type Source interface {
	// Commitments lists live blobs: size and expiry (unix seconds).
	Commitments(ctx context.Context, now int64) ([]Commitment, error)
	// RecentSizes lists the sizes of blobs stored since a unix time, used to
	// seed the inflow estimate at startup.
	RecentSizes(ctx context.Context, since int64) ([]int64, error)
}

// Commitment is one live blob.
type Commitment struct {
	Size   int64
	Expiry int64
}

// ErrFull means accepting the file would take the store past its hard fill.
var ErrFull = errors.New("storage is full")

const (
	sizeMin   = 1024 // sizes below this are treated as 1 KB
	perOctave = 4    // size buckets per doubling
	firstBin  = 10 * perOctave
	lastBin   = 32 * perOctave // 4 GB
	numBins   = lastBin - firstBin + 1
	maxQuotes = 50000
	histMax   = 7 * 24 * 12 // a week at five-minute ticks
)

// horizons are the future points the projection is checked at.
var horizons = []time.Duration{
	time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour,
	24 * time.Hour, 2 * 24 * time.Hour, 4 * 24 * time.Hour, 7 * 24 * time.Hour,
	14 * 24 * time.Hour, 30 * 24 * time.Hour, 60 * 24 * time.Hour, 120 * 24 * time.Hour,
	182 * 24 * time.Hour, 365 * 24 * time.Hour,
}

// Quote is a held expiry for one file.
type Quote struct {
	Sha256    string `json:"sha256"`
	Size      int64  `json:"size"`
	ExpiresAt int64  `json:"expires_at"`
	Retention int64  `json:"retention"` // seconds from the moment of quoting
	HoldUntil int64  `json:"hold_until"`
}

// Point is one sample of the store's state for the live chart.
type Point struct {
	T     int64   `json:"t"`
	Fill  float64 `json:"fill"`
	Theta float64 `json:"theta"`
}

// Controller holds the live estimate and theta. It is safe for concurrent use.
type Controller struct {
	src Source

	mu        sync.Mutex
	p         Params
	theta     float64 // seconds
	thetaSet  bool
	inflow    float64 // offered bytes per second, smoothed
	weights   [numBins]float64
	pendBytes float64
	pendBins  [numBins]float64
	lastTick  time.Time
	used      int64
	nextFree  int64 // unix seconds of the next expiry, 0 if none
	quotes    map[string]Quote
	history   []Point
	updated   time.Time
}

// New returns a controller over src. Call Seed once, then Run.
func New(p Params, src Source) *Controller {
	c := &Controller{src: src, quotes: map[string]Quote{}}
	c.p = normalize(p)
	return c
}

func normalize(p Params) Params {
	if p.TargetFill <= 0 || p.TargetFill >= 1 {
		p.TargetFill = 0.7
	}
	if p.HardFill <= p.TargetFill || p.HardFill > 1 {
		p.HardFill = math.Min(0.95, p.TargetFill+0.2)
	}
	if p.TTLMin <= 0 {
		p.TTLMin = 24 * time.Hour
	}
	if p.TTLMax < p.TTLMin {
		p.TTLMax = p.TTLMin
	}
	if p.Alpha < 0 {
		p.Alpha = 0
	}
	if p.SizeRefBytes <= 0 {
		p.SizeRefBytes = 1 << 20
	}
	if p.SizeMaxBytes <= 0 {
		p.SizeMaxBytes = 50 << 20
	}
	if p.QuoteHold <= 0 {
		p.QuoteHold = 15 * time.Minute
	}
	if p.MaxRisePerDay < 1 {
		p.MaxRisePerDay = 2
	}
	if p.EWMAHalfLife <= 0 {
		p.EWMAHalfLife = 24 * time.Hour
	}
	return p
}

// SetParams applies new bounds; theta is re-solved on the next tick.
func (c *Controller) SetParams(p Params) {
	c.mu.Lock()
	c.p = normalize(p)
	c.mu.Unlock()
}

// ── the lifetime function ──────────────────────────────────────────────

func (p Params) shape(size float64) float64 {
	if size < sizeMin {
		size = sizeMin
	}
	return math.Pow(float64(p.SizeRefBytes)/size, p.Alpha)
}

func (p Params) thetaLo() float64 { return p.TTLMin.Seconds() / p.shape(sizeMin) }
func (p Params) thetaHi() float64 { return p.TTLMax.Seconds() / p.shape(float64(p.SizeMaxBytes)) }

func (p Params) lifetime(size float64, theta float64) float64 {
	t := theta * p.shape(size)
	return math.Max(p.TTLMin.Seconds(), math.Min(p.TTLMax.Seconds(), t))
}

func binOf(size int64) int {
	if size < sizeMin {
		size = sizeMin
	}
	b := int(math.Log2(float64(size))*perOctave) - firstBin
	if b < 0 {
		b = 0
	}
	if b >= numBins {
		b = numBins - 1
	}
	return b
}

// binSize is the geometric middle of a bucket.
func binSize(b int) float64 { return math.Pow(2, (float64(b+firstBin)+0.5)/perOctave) }

// Lifetime is the retention a file of this size would get right now.
func (c *Controller) Lifetime(size int64) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Duration(c.p.lifetime(float64(size), c.currentTheta()) * float64(time.Second))
}

func (c *Controller) currentTheta() float64 {
	if !c.thetaSet {
		return c.p.thetaHi()
	}
	return c.theta
}

// ── inflow and admission ───────────────────────────────────────────────

// Offer records an upload attempt, accepted or not, so the inflow estimate
// sees demand rather than only what fit.
func (c *Controller) Offer(size int64) {
	if size <= 0 {
		return
	}
	c.mu.Lock()
	c.pendBytes += float64(size)
	c.pendBins[binOf(size)] += float64(size)
	c.mu.Unlock()
}

// Admit refuses a file that would take the store past its hard fill.
func (c *Controller) Admit(size int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if float64(c.used+size) > c.p.HardFill*float64(c.p.CapacityBytes) {
		return ErrFull
	}
	return nil
}

// Commit records a stored file until the next tick reloads the truth.
func (c *Controller) Commit(size int64) {
	c.mu.Lock()
	c.used += size
	c.mu.Unlock()
}

// Release records a deleted file until the next tick.
func (c *Controller) Release(size int64) {
	c.mu.Lock()
	c.used -= size
	if c.used < 0 {
		c.used = 0
	}
	c.mu.Unlock()
}

// RetryAfter is how long until the next stored file expires, for a 507.
func (c *Controller) RetryAfter(now time.Time) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.nextFree <= now.Unix() {
		return time.Hour
	}
	return time.Duration(c.nextFree-now.Unix()) * time.Second
}

// ── quotes ─────────────────────────────────────────────────────────────

// Quote fixes an expiry for a file and holds it for QuoteHold. Asking again
// for the same file and size while the hold lasts returns the same quote, so
// a quote cannot be shopped for a better one by repeating it.
func (c *Controller) Quote(sha256 string, size int64, now time.Time) Quote {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q, ok := c.quotes[sha256]; ok && q.Size == size && q.HoldUntil > now.Unix() {
		return q
	}
	if len(c.quotes) >= maxQuotes {
		c.purgeQuotes(now)
		if len(c.quotes) >= maxQuotes {
			for k := range c.quotes { // drop an arbitrary one
				delete(c.quotes, k)
				break
			}
		}
	}
	life := int64(c.p.lifetime(float64(size), c.currentTheta()))
	q := Quote{
		Sha256:    sha256,
		Size:      size,
		ExpiresAt: now.Unix() + life,
		Retention: life,
		HoldUntil: now.Add(c.p.QuoteHold).Unix(),
	}
	c.quotes[sha256] = q
	return q
}

// ExpiryFor is the expiry to promise a file being stored now: its held quote
// when one is still valid for this exact size, otherwise the current
// lifetime. A used quote is consumed.
func (c *Controller) ExpiryFor(sha256 string, size int64, now time.Time) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if q, ok := c.quotes[sha256]; ok {
		delete(c.quotes, sha256)
		if q.Size == size && q.HoldUntil >= now.Unix() {
			return q.ExpiresAt
		}
	}
	return now.Unix() + int64(c.p.lifetime(float64(size), c.currentTheta()))
}

func (c *Controller) purgeQuotes(now time.Time) {
	for k, q := range c.quotes {
		if q.HoldUntil < now.Unix() {
			delete(c.quotes, k)
		}
	}
}

// ── the control loop ───────────────────────────────────────────────────

// Seed loads the store's state and starts the inflow estimate from the last
// day's accepted uploads, so a restart does not begin from zero traffic.
func (c *Controller) Seed(ctx context.Context, now time.Time) error {
	sizes, err := c.src.RecentSizes(ctx, now.Add(-7*24*time.Hour).Unix())
	if err != nil {
		return err
	}
	var weekBytes float64
	var bins [numBins]float64
	for _, s := range sizes {
		weekBytes += float64(s)
		bins[binOf(s)] += float64(s)
	}
	c.mu.Lock()
	c.inflow = weekBytes / (7 * 24 * 3600)
	if weekBytes > 0 {
		for i := range bins {
			c.weights[i] = bins[i] / weekBytes
		}
	} else {
		c.weights[binOf(c.p.SizeRefBytes)] = 1
	}
	c.lastTick = now
	c.mu.Unlock()
	return c.Tick(ctx, now)
}

// Run ticks every interval until ctx ends.
func (c *Controller) Run(ctx context.Context, interval time.Duration, onErr func(error)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if err := c.Tick(ctx, now); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}

// Tick reloads what is stored, updates the inflow estimate and re-solves theta.
func (c *Controller) Tick(ctx context.Context, now time.Time) error {
	items, err := c.src.Commitments(ctx, now.Unix())
	if err != nil {
		return err
	}

	// Committed occupancy at each horizon, and the next expiry.
	committed := make([]float64, len(horizons))
	var used int64
	var next int64
	for _, it := range items {
		used += it.Size
		if it.Expiry <= 0 { // no expiry: held for good
			for k := range horizons {
				committed[k] += float64(it.Size)
			}
			continue
		}
		left := it.Expiry - now.Unix()
		if next == 0 || it.Expiry < next {
			next = it.Expiry
		}
		for k, h := range horizons {
			if left > int64(h.Seconds()) {
				committed[k] += float64(it.Size)
			}
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	dt := now.Sub(c.lastTick).Seconds()
	if dt > 0 {
		a := 1 - math.Pow(0.5, dt/c.p.EWMAHalfLife.Seconds())
		c.inflow += a * (c.pendBytes/dt - c.inflow)
		if c.pendBytes > 0 {
			for i := range c.weights {
				c.weights[i] += a * (c.pendBins[i]/c.pendBytes - c.weights[i])
			}
		}
	}
	c.pendBytes = 0
	c.pendBins = [numBins]float64{}
	c.lastTick = now
	c.used = used
	c.nextFree = next
	c.purgeQuotes(now)

	target := c.solve(committed)
	if c.thetaSet && dt > 0 {
		// Fall at once, rise slowly: a short lull must not hand out long
		// promises just before traffic comes back.
		limit := c.theta * math.Pow(c.p.MaxRisePerDay, dt/86400)
		target = math.Min(target, limit)
	}
	c.theta = math.Max(c.p.thetaLo(), math.Min(c.p.thetaHi(), target))
	c.thetaSet = true
	c.updated = now

	c.history = append(c.history, Point{T: now.Unix(), Fill: c.fill(), Theta: c.theta})
	if len(c.history) > histMax {
		c.history = c.history[len(c.history)-histMax:]
	}
	return nil
}

// solve finds the largest theta whose projected occupancy stays at or under
// the target at every horizon. Caller holds mu.
func (c *Controller) solve(committed []float64) float64 {
	limit := c.p.TargetFill * float64(c.p.CapacityBytes)
	ok := func(theta float64) bool {
		for k, h := range horizons {
			tau := h.Seconds()
			var stream float64
			for b, w := range c.weights {
				if w > 0 {
					stream += w * math.Min(tau, c.p.lifetime(binSize(b), theta))
				}
			}
			if committed[k]+c.inflow*stream > limit {
				return false
			}
		}
		return true
	}
	lo, hi := c.p.thetaLo(), c.p.thetaHi()
	if !ok(lo) {
		return lo
	}
	if ok(hi) {
		return hi
	}
	l, h := math.Log(lo), math.Log(hi)
	for i := 0; i < 40; i++ {
		m := (l + h) / 2
		if ok(math.Exp(m)) {
			l = m
		} else {
			h = m
		}
	}
	return math.Exp(l)
}

func (c *Controller) fill() float64 {
	if c.p.CapacityBytes <= 0 {
		return 0
	}
	return float64(c.used) / float64(c.p.CapacityBytes)
}

// ── published state ────────────────────────────────────────────────────

// CurvePoint is one point of the live retention curve.
type CurvePoint struct {
	Size      int64 `json:"size"`
	Retention int64 `json:"retention"` // seconds
}

// State is what the store publishes about itself.
type State struct {
	CapacityBytes int64        `json:"capacity"`
	UsedBytes     int64        `json:"used"`
	Fill          float64      `json:"fill"`
	TargetFill    float64      `json:"target_fill"`
	HardFill      float64      `json:"hard_fill"`
	TTLMin        int64        `json:"ttl_min"`
	TTLMax        int64        `json:"ttl_max"`
	Shape         Shape        `json:"shape"`
	Theta         float64      `json:"theta"`
	SizeMax       int64        `json:"size_max"`
	InflowPerDay  float64      `json:"inflow_bytes_per_day"`
	QuoteHold     int64        `json:"quote_hold"`
	NextExpiry    int64        `json:"next_expiry,omitempty"`
	Curve         []CurvePoint `json:"curve"`
	History       []Point      `json:"history"`
	UpdatedAt     int64        `json:"updated_at"`
}

// Shape names the size dependence so a client can compute any lifetime.
type Shape struct {
	Kind  string  `json:"kind"`
	SRef  int64   `json:"s_ref"`
	Alpha float64 `json:"alpha"`
}

// State reports the current state, with the curve sampled on a log grid and
// the history thinned to at most maxPoints.
func (c *Controller) State(maxPoints int) State {
	c.mu.Lock()
	defer c.mu.Unlock()
	theta := c.currentTheta()
	var curve []CurvePoint
	for s := float64(sizeMin); s < float64(c.p.SizeMaxBytes); s *= 1.5 {
		curve = append(curve, CurvePoint{Size: int64(s), Retention: int64(c.p.lifetime(s, theta))})
	}
	curve = append(curve, CurvePoint{Size: c.p.SizeMaxBytes, Retention: int64(c.p.lifetime(float64(c.p.SizeMaxBytes), theta))})

	hist := c.history
	if maxPoints > 0 && len(hist) > maxPoints {
		step := float64(len(hist)) / float64(maxPoints)
		thin := make([]Point, 0, maxPoints)
		for i := 0; i < maxPoints; i++ {
			thin = append(thin, hist[int(float64(i)*step)])
		}
		thin[len(thin)-1] = hist[len(hist)-1]
		hist = thin
	} else {
		hist = append([]Point(nil), hist...)
	}
	sort.Slice(hist, func(i, j int) bool { return hist[i].T < hist[j].T })

	return State{
		CapacityBytes: c.p.CapacityBytes,
		UsedBytes:     c.used,
		Fill:          c.fill(),
		TargetFill:    c.p.TargetFill,
		HardFill:      c.p.HardFill,
		TTLMin:        int64(c.p.TTLMin.Seconds()),
		TTLMax:        int64(c.p.TTLMax.Seconds()),
		Shape:         Shape{Kind: "power", SRef: c.p.SizeRefBytes, Alpha: c.p.Alpha},
		Theta:         theta,
		SizeMax:       c.p.SizeMaxBytes,
		InflowPerDay:  c.inflow * 86400,
		QuoteHold:     int64(c.p.QuoteHold.Seconds()),
		NextExpiry:    c.nextFree,
		Curve:         curve,
		History:       hist,
		UpdatedAt:     c.updated.Unix(),
	}
}
