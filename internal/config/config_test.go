package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestRateLimitValidate(t *testing.T) {
	const maxUpload = 50 << 20
	cases := []struct {
		name    string
		cfg     RateLimitConfig
		wantErr string
	}{
		{"all off is fine", RateLimitConfig{}, ""},
		{"anonymous needs the limiter", RateLimitConfig{AnonymousUploads: true}, "requires rate_limit.enabled"},
		{"enabled needs a limit", RateLimitConfig{Enabled: true}, "at least one of"},
		{"budget below the file size limit", RateLimitConfig{Enabled: true, BytesPerWindowPerIP: 10 << 20}, "at least max_upload_size_bytes"},
		{"bad window", RateLimitConfig{Enabled: true, UploadsPerMinutePerIP: 1, Window: "soon"}, "rate_limit.window"},
		{"negative window", RateLimitConfig{Enabled: true, UploadsPerMinutePerIP: 1, Window: "-1h"}, "rate_limit.window"},
		{"negative limit", RateLimitConfig{UploadsPerMinutePerIP: -1}, "cannot be negative"},
		{"bad proxy", RateLimitConfig{TrustedProxies: []string{"proxy"}}, "not an IP address"},
		{"bad cidr", RateLimitConfig{TrustedProxies: []string{"10.0.0.0/99"}}, "not a CIDR"},
		{"good", RateLimitConfig{Enabled: true, AnonymousUploads: true, UploadsPerMinutePerIP: 10, BytesPerWindowPerIP: 500 << 20, Window: "24h", MaxConcurrentPerIP: 3, TrustedProxies: []string{"10.0.0.0/8", "::1"}}, ""},
		{"good with only concurrency", RateLimitConfig{Enabled: true, MaxConcurrentPerIP: 1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate(maxUpload)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("got %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestRateLimitWindowDuration(t *testing.T) {
	if got := (RateLimitConfig{}).WindowDuration(); got != DefaultRateLimitWindow {
		t.Fatalf("empty window = %s, want %s", got, DefaultRateLimitWindow)
	}
	if got := (RateLimitConfig{Window: " 90m "}).WindowDuration(); got != 90*time.Minute {
		t.Fatalf("90m window = %s", got)
	}
}

func TestRateLimitBlockOmittedWhileOff(t *testing.T) {
	out, err := yaml.Marshal(&Config{AllowedMimeTypes: []string{"*"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "rate_limit") {
		t.Fatalf("a zero rate_limit block was written:\n%s", out)
	}
	out, err = yaml.Marshal(&Config{AllowedMimeTypes: []string{"*"}, RateLimit: RateLimitConfig{Enabled: true, UploadsPerMinutePerIP: 5}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "uploads_per_minute_per_ip: 5") {
		t.Fatalf("enabled block missing:\n%s", out)
	}
}

func TestDefaultConfigValidates(t *testing.T) {
	cfg := &Config{}
	if err := yaml.Unmarshal(DefaultYAML, cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.RateLimit.Enabled || cfg.RateLimit.AnonymousUploads {
		t.Fatal("the default config must ship with the limiter off")
	}
	if cfg.RateLimit.BytesPerWindowPerIP < int64(cfg.MaxUploadSizeBytes) {
		t.Fatal("the documented default budget would be rejected once enabled")
	}
	cfg.RateLimit.Enabled = true
	cfg.RateLimit.AnonymousUploads = true
	if err := cfg.Validate(); err != nil {
		t.Fatalf("the documented defaults do not validate when switched on: %v", err)
	}
}

func TestStoreUpdateKeepsRateLimitBlock(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	src := `
db_path: "db/database.sqlite3"
log_level: "INFO"
api_addr: "127.0.0.1:0"
cdn_url: "http://localhost:0"
max_upload_size_bytes: 1048576
access_control_rules:
  - action: "ALLOW"
    pubkey: "ALL"
    resource: "GET"
allowed_mime_types:
  - "*"
rate_limit:
  enabled: true
  anonymous_uploads: true
  uploads_per_minute_per_ip: 7
  bytes_per_window_per_ip: 2097152
  window: "12h"
  trusted_proxies: ["10.0.0.0/8"]
`
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	store, created, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("Load wrote a default over an existing file")
	}
	var applied []RateLimitConfig
	store.SetApplyHook(func(c Config) error {
		applied = append(applied, c.RateLimit)
		return nil
	})
	if err := store.Update(func(c *Config) error {
		c.Server.Name = "renamed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(applied) != 1 || !applied[0].Enabled || applied[0].UploadsPerMinutePerIP != 7 || applied[0].Window != "12h" {
		t.Fatalf("apply hook saw %+v", applied)
	}
	reloaded, err := NewConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	rl := reloaded.RateLimit
	if !rl.Enabled || !rl.AnonymousUploads || rl.UploadsPerMinutePerIP != 7 || rl.BytesPerWindowPerIP != 2097152 || rl.Window != "12h" || len(rl.TrustedProxies) != 1 {
		t.Fatalf("rate_limit block did not survive the write-back: %+v", rl)
	}

	// Turning anonymous uploads on without the limiter is refused and the
	// file is left alone.
	err = store.Update(func(c *Config) error {
		c.RateLimit.Enabled = false
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "requires rate_limit.enabled") {
		t.Fatalf("expected the dependency to be enforced on update, got %v", err)
	}
}
