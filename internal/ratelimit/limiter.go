// Package ratelimit bounds uploads per client network address so that a
// server open to throwaway keys cannot be drained by minting keys. It is the
// server-side half of anonymous uploads: every upload on the Blossom path,
// signed by a fresh key or a real one, is charged to the network it came
// from.
//
// Privacy: the limiter never stores a network address. Each address and each
// pubkey is reduced to an HMAC keyed with a secret drawn at startup and kept
// only in memory, and the tables live in memory only. A restart unlinks
// everything; nothing reaches the database, the log or the disk. The
// pubkey→address binding that charges a key's uploads to the address it was
// first seen from expires after one budget window, so no association outlives
// a window.
//
// Limits are token buckets: a per-minute upload count, a byte budget that
// refills evenly over the window (so "500 MB per day" means a burst of up to
// 500 MB, then a steady 500 MB per day), and a cap on uploads in flight.
package ratelimit

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"math"
	"sync"
	"time"
)

// Config is the limiter's live configuration. Zero limits disable that
// dimension; Enabled false disables the limiter entirely.
type Config struct {
	Enabled bool
	// UploadsPerMinute per address, also the burst size.
	UploadsPerMinute int
	// BytesPerWindow per address: the burst size, refilled evenly over Window.
	BytesPerWindow int64
	Window         time.Duration
	// MaxConcurrent uploads in flight per address.
	MaxConcurrent int
	// ExemptTrusted skips the limiter for pubkeys the operator vouches for
	// (an explicit access rule or a nostr.json membership).
	ExemptTrusted bool
	// TrustedProxies are addresses or CIDRs whose forwarded-client headers are
	// believed, in addition to loopback.
	TrustedProxies []string
}

// DefaultWindow is used when a configuration names none.
const DefaultWindow = 24 * time.Hour

// maxBuckets bounds the address table; beyond it idle entries are evicted,
// which only ever makes the limiter more lenient.
const maxBuckets = 250_000

// concurrencyRetry is the Retry-After for a concurrency denial: uploads in
// flight finish in seconds, not minutes.
const concurrencyRetry = 5 * time.Second

// Denial explains a refused request.
type Denial struct {
	Reason     string
	RetryAfter time.Duration
}

// RetryAfterSeconds is the value for the Retry-After header, at least 1.
func (d *Denial) RetryAfterSeconds() int {
	return max(1, int(math.Ceil(d.RetryAfter.Seconds())))
}

// Limiter is safe for concurrent use.
type Limiter struct {
	mu        sync.Mutex
	cfg       Config
	proxies   []proxyNet
	secret    []byte
	addrs     map[string]*bucket
	keys      map[string]binding
	lastSweep time.Time
	now       func() time.Time
}

type bucket struct {
	uploads  float64
	bytes    float64
	updated  time.Time
	inflight int
}

type binding struct {
	addr    string
	expires time.Time
}

// New builds a limiter with a fresh random secret.
func New(cfg Config) (*Limiter, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("rate limiter secret: %w", err)
	}
	l := &Limiter{
		secret: secret,
		addrs:  map[string]*bucket{},
		keys:   map[string]binding{},
		now:    time.Now,
	}
	if err := l.Reload(cfg); err != nil {
		return nil, err
	}
	return l, nil
}

// Reload adopts a new configuration. Existing buckets keep their levels and
// clamp to the new capacities as they refill; the secret is kept so bindings
// survive a hot apply.
func (l *Limiter) Reload(cfg Config) error {
	proxies, err := parseProxies(cfg.TrustedProxies)
	if err != nil {
		return err
	}
	if cfg.Window <= 0 {
		cfg.Window = DefaultWindow
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cfg = cfg
	l.proxies = proxies
	return nil
}

// Config returns the live configuration.
func (l *Limiter) Config() Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cfg
}

// Enabled reports whether requests are being limited.
func (l *Limiter) Enabled() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.cfg.Enabled
}

// Reservation is a charged upload attempt. Call Release when the request is
// finished and Settle with the number of bytes actually received.
type Reservation struct {
	l        *Limiter
	direct   string
	buckets  []string
	reserved int64
	released bool
}

// Reserve charges one upload attempt of size bytes (size <= 0 when unknown)
// by pubkey from addr, or explains why it is refused. A pubkey seen for the
// first time is bound to addr for one window; while bound, its uploads from
// any other address are charged to both addresses.
func (l *Limiter) Reserve(addr, pubkey string, size int64) (*Reservation, *Denial) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.maybeSweep(now)
	if !l.cfg.Enabled {
		return &Reservation{}, nil
	}

	ak := l.hash("addr", addr)
	direct := l.bucket(ak, now)
	if l.cfg.MaxConcurrent > 0 && direct.inflight >= l.cfg.MaxConcurrent {
		return nil, l.concurrencyDenial()
	}

	keys := []string{ak}
	buckets := []*bucket{direct}
	if pubkey != "" {
		pk := l.hash("pubkey", pubkey)
		if bnd, ok := l.keys[pk]; ok && now.Before(bnd.expires) {
			if bnd.addr != ak {
				keys = append(keys, bnd.addr)
				buckets = append(buckets, l.bucket(bnd.addr, now))
			}
		} else {
			l.keys[pk] = binding{addr: ak, expires: now.Add(l.cfg.Window)}
		}
	}

	for _, b := range buckets {
		if d := l.check(b, size); d != nil {
			return nil, d
		}
	}

	reserved := int64(0)
	if size > 0 {
		reserved = size
	}
	for _, b := range buckets {
		l.charge(b, reserved)
	}
	direct.inflight++
	return &Reservation{l: l, direct: ak, buckets: keys, reserved: reserved}, nil
}

// Peek reports whether Reserve would refuse the request, without charging
// anything or binding the pubkey. HEAD /upload uses it.
func (l *Limiter) Peek(addr, pubkey string, size int64) *Denial {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.cfg.Enabled {
		return nil
	}
	now := l.now()
	ak := l.hash("addr", addr)
	direct := l.bucket(ak, now)
	if l.cfg.MaxConcurrent > 0 && direct.inflight >= l.cfg.MaxConcurrent {
		return l.concurrencyDenial()
	}
	if d := l.check(direct, size); d != nil {
		return d
	}
	if pubkey != "" {
		if bnd, ok := l.keys[l.hash("pubkey", pubkey)]; ok && now.Before(bnd.expires) && bnd.addr != ak {
			return l.check(l.bucket(bnd.addr, now), size)
		}
	}
	return nil
}

// Settle charges any bytes received beyond what Reserve was told. Bytes
// reserved but never sent stay charged: the budget counts what was asked for.
func (r *Reservation) Settle(actual int64) {
	if r.l == nil || actual <= r.reserved {
		return
	}
	extra := actual - r.reserved
	r.reserved = actual
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	now := r.l.now()
	for _, k := range r.buckets {
		b := r.l.bucket(k, now)
		if r.l.cfg.BytesPerWindow > 0 {
			b.bytes -= float64(extra)
		}
	}
}

// Release frees the in-flight slot. Safe to call more than once.
func (r *Reservation) Release() {
	if r.l == nil || r.released {
		return
	}
	r.released = true
	r.l.mu.Lock()
	defer r.l.mu.Unlock()
	if b, ok := r.l.addrs[r.direct]; ok && b.inflight > 0 {
		b.inflight--
	}
}

// hash keys a table entry. The secret never leaves memory, so the tables are
// not reversible to addresses or pubkeys by anyone holding a memory dump
// without also holding the running process.
func (l *Limiter) hash(kind, v string) string {
	m := hmac.New(sha256.New, l.secret)
	m.Write([]byte(kind))
	m.Write([]byte{0})
	m.Write([]byte(v))
	return string(m.Sum(nil)[:16])
}

// bucket returns the entry for key, full when new, refilled otherwise.
func (l *Limiter) bucket(key string, now time.Time) *bucket {
	b, ok := l.addrs[key]
	if !ok {
		b = &bucket{
			uploads: float64(l.cfg.UploadsPerMinute),
			bytes:   float64(l.cfg.BytesPerWindow),
			updated: now,
		}
		l.addrs[key] = b
		return b
	}
	l.refill(b, now)
	return b
}

func (l *Limiter) refill(b *bucket, now time.Time) {
	dt := now.Sub(b.updated).Seconds()
	if dt < 0 {
		dt = 0
	}
	capU := float64(l.cfg.UploadsPerMinute)
	b.uploads = math.Min(capU, b.uploads+dt*capU/60)
	capB := float64(l.cfg.BytesPerWindow)
	b.bytes = math.Min(capB, b.bytes+dt*capB/l.cfg.Window.Seconds())
	b.updated = now
}

func (l *Limiter) concurrencyDenial() *Denial {
	return &Denial{
		Reason:     fmt.Sprintf("Too many uploads in progress from your network: the limit is %d at a time.", l.cfg.MaxConcurrent),
		RetryAfter: concurrencyRetry,
	}
}

// check evaluates the count and byte limits of one bucket without charging.
func (l *Limiter) check(b *bucket, size int64) *Denial {
	cfg := l.cfg
	if cfg.UploadsPerMinute > 0 && b.uploads < 1 {
		wait := (1 - b.uploads) * 60 / float64(cfg.UploadsPerMinute)
		return &Denial{
			Reason:     fmt.Sprintf("Too many uploads from your network: the limit is %d per minute.", cfg.UploadsPerMinute),
			RetryAfter: time.Duration(wait * float64(time.Second)),
		}
	}
	if cfg.BytesPerWindow > 0 {
		need := float64(size)
		if need <= 0 {
			need = 1
		}
		budget := HumanBytes(cfg.BytesPerWindow) + " per " + HumanDuration(cfg.Window)
		if need > float64(cfg.BytesPerWindow) {
			return &Denial{
				Reason:     fmt.Sprintf("File is larger than your network's upload budget of %s.", budget),
				RetryAfter: cfg.Window,
			}
		}
		if b.bytes < need {
			perSec := float64(cfg.BytesPerWindow) / cfg.Window.Seconds()
			wait := (need - b.bytes) / perSec
			return &Denial{
				Reason:     fmt.Sprintf("Your network's upload budget of %s is used up.", budget),
				RetryAfter: time.Duration(wait * float64(time.Second)),
			}
		}
	}
	return nil
}

func (l *Limiter) charge(b *bucket, bytes int64) {
	if l.cfg.UploadsPerMinute > 0 {
		b.uploads--
	}
	if l.cfg.BytesPerWindow > 0 {
		b.bytes -= float64(bytes)
	}
}

// maybeSweep drops idle buckets and expired bindings about once a minute, or
// whenever the address table is over its cap.
func (l *Limiter) maybeSweep(now time.Time) {
	if now.Sub(l.lastSweep) < time.Minute && len(l.addrs) <= maxBuckets {
		return
	}
	l.lastSweep = now
	for k, b := range l.addrs {
		if b.inflight == 0 && now.Sub(b.updated) >= l.cfg.Window && now.Sub(b.updated) >= time.Minute {
			delete(l.addrs, k)
		}
	}
	for k, bnd := range l.keys {
		if !now.Before(bnd.expires) {
			delete(l.keys, k)
		}
	}
	if len(l.addrs) > maxBuckets {
		target := maxBuckets * 9 / 10
		for k, b := range l.addrs {
			if len(l.addrs) <= target {
				break
			}
			if b.inflight == 0 {
				delete(l.addrs, k)
			}
		}
	}
}

// Size reports the table sizes, for tests and diagnostics.
func (l *Limiter) Size() (addrs, bindings int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.addrs), len(l.keys)
}
