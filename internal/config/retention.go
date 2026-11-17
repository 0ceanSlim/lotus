package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// RetentionConfig turns on self-regulating retention for a 0x0-backed
// deployment: each upload gets an expiry chosen so the store holds its usage
// near target_fill of capacity_bytes, and that expiry is quoted before the
// upload and kept. See docs/self-regulating-storage.
type RetentionConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// CapacityBytes is the space this store may use.
	CapacityBytes int64 `yaml:"capacity_bytes" json:"capacity_bytes"`
	// TargetFill is the fraction of capacity the controller steers toward.
	TargetFill float64 `yaml:"target_fill" json:"target_fill"`
	// HardFill is the fraction above which uploads are refused (507).
	HardFill float64 `yaml:"hard_fill" json:"hard_fill"`
	// TTLMin and TTLMax bound every lifetime, as Go durations ("24h",
	// "8760h"). TTLMin is a promise; TTLMax is a ceiling.
	TTLMin string `yaml:"ttl_min" json:"ttl_min"`
	TTLMax string `yaml:"ttl_max" json:"ttl_max"`
	// Alpha is how size trades against time: 0 gives every size the same
	// lifetime, 1 gives every file the same bytes x time.
	Alpha float64 `yaml:"alpha" json:"alpha"`
	// SizeRefBytes is the size the published time scale refers to.
	SizeRefBytes int64 `yaml:"size_ref_bytes" json:"size_ref_bytes"`
	// QuoteHold is how long a quoted expiry is held for the uploader.
	QuoteHold string `yaml:"quote_hold" json:"quote_hold"`
	// MaxRisePerDay caps how fast lifetimes may grow after traffic falls.
	MaxRisePerDay float64 `yaml:"max_rise_per_day" json:"max_rise_per_day"`
}

// Retention defaults, used for empty fields.
const (
	DefaultTTLMin    = 24 * time.Hour
	DefaultTTLMax    = 365 * 24 * time.Hour
	DefaultQuoteHold = 15 * time.Minute
)

func parseDurationOr(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(s)); err == nil && d > 0 {
		return d
	}
	return def
}

// TTLMinDuration is the parsed minimum lifetime.
func (r RetentionConfig) TTLMinDuration() time.Duration {
	return parseDurationOr(r.TTLMin, DefaultTTLMin)
}

// TTLMaxDuration is the parsed maximum lifetime.
func (r RetentionConfig) TTLMaxDuration() time.Duration {
	return parseDurationOr(r.TTLMax, DefaultTTLMax)
}

// QuoteHoldDuration is the parsed quote hold.
func (r RetentionConfig) QuoteHoldDuration() time.Duration {
	return parseDurationOr(r.QuoteHold, DefaultQuoteHold)
}

// Validate checks the block when it is enabled.
func (r RetentionConfig) Validate(zeroXZero bool) error {
	if !r.Enabled {
		return nil
	}
	if !zeroXZero {
		return errors.New("retention needs zero_x_zero.enabled: it manages expiring storage")
	}
	if r.CapacityBytes <= 0 {
		return errors.New("retention.capacity_bytes must be positive")
	}
	if r.TargetFill <= 0 || r.TargetFill >= 1 {
		return errors.New("retention.target_fill must be between 0 and 1")
	}
	if r.HardFill <= r.TargetFill || r.HardFill > 1 {
		return errors.New("retention.hard_fill must be above target_fill and at most 1")
	}
	for name, v := range map[string]string{"ttl_min": r.TTLMin, "ttl_max": r.TTLMax, "quote_hold": r.QuoteHold} {
		if v = strings.TrimSpace(v); v != "" {
			if d, err := time.ParseDuration(v); err != nil || d <= 0 {
				return fmt.Errorf("retention.%s must be a positive duration such as \"24h\", got %q", name, v)
			}
		}
	}
	if r.TTLMinDuration() > r.TTLMaxDuration() {
		return errors.New("retention.ttl_min cannot be longer than ttl_max")
	}
	if r.Alpha < 0 || r.Alpha > 2 {
		return errors.New("retention.alpha must be between 0 and 2")
	}
	if r.MaxRisePerDay != 0 && r.MaxRisePerDay < 1 {
		return errors.New("retention.max_rise_per_day must be at least 1")
	}
	return nil
}

// OnionConfig serves the same site on a second, loopback-only listener for a
// Tor hidden service. Requests there are told apart from clearnet ones: they
// all share one upload budget (Tor hides the client), and forwarded-address
// headers on them are ignored so nobody can claim another address.
type OnionConfig struct {
	// ListenAddr is where the hidden service forwards to, e.g.
	// "127.0.0.1:8005". Empty disables the listener.
	ListenAddr string `yaml:"listen_addr" json:"listen_addr"`
	// Url is the http://….onion address, advertised to clearnet visitors in
	// an Onion-Location header so Tor Browser offers to switch.
	Url string `yaml:"url" json:"url"`
}

// Validate checks the onion block.
func (o OnionConfig) Validate() error {
	if a := strings.TrimSpace(o.ListenAddr); a != "" && !strings.HasPrefix(a, "127.0.0.1:") && !strings.HasPrefix(a, "[::1]:") && !strings.HasPrefix(a, "localhost:") {
		return errors.New("onion.listen_addr must be a loopback address such as 127.0.0.1:8005: Tor connects locally, and anything else would let clearnet clients share the onion budget")
	}
	if u := strings.TrimSpace(o.Url); u != "" && (!strings.HasPrefix(u, "http://") || !strings.HasSuffix(strings.TrimRight(u, "/"), ".onion")) {
		return errors.New("onion.url must look like http://<address>.onion")
	}
	return nil
}
