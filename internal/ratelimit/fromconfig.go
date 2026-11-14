package ratelimit

import "github.com/0ceanSlim/lotus/internal/config"

// FromConfig maps the config.yml block onto a limiter configuration.
func FromConfig(c config.RateLimitConfig) Config {
	return Config{
		Enabled:          c.Enabled,
		UploadsPerMinute: c.UploadsPerMinutePerIP,
		BytesPerWindow:   c.BytesPerWindowPerIP,
		Window:           c.WindowDuration(),
		MaxConcurrent:    c.MaxConcurrentPerIP,
		ExemptTrusted:    c.ExemptTrustedPubkeys,
		TrustedProxies:   append([]string(nil), c.TrustedProxies...),
	}
}
