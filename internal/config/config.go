// Package config loads, validates, serves and persists lotus's config.yml.
//
// The Store is the single owner of the running configuration. Handlers read
// a snapshot with Get; the admin page and the first-run claim mutate it with
// Update, which validates, lets the services apply the new values, writes
// the file back, and only then publishes the change.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed default.yml
var DefaultYAML []byte

type AccessControlRule struct {
	Action   string `yaml:"action" json:"action"`
	Pubkey   string `yaml:"pubkey" json:"pubkey"`
	Resource string `yaml:"resource" json:"resource"`
}

type ZeroXZeroConfig struct {
	Enabled          bool    `yaml:"enabled" json:"enabled"`
	InstanceUrl      string  `yaml:"instance_url" json:"instance_url"`
	MaxFileSizeBytes int64   `yaml:"max_file_size_bytes" json:"max_file_size_bytes"`
	MinRetentionDays float64 `yaml:"min_retention_days" json:"min_retention_days"`
	MaxRetentionDays float64 `yaml:"max_retention_days" json:"max_retention_days"`
}

// ServerInfo is the operator-facing identity of this deployment. Everything is
// optional; the landing page and /api/v1/server/info render whatever is set.
type ServerInfo struct {
	Name          string `yaml:"name" json:"name"`
	Description   string `yaml:"description" json:"description"`
	Icon          string `yaml:"icon" json:"icon"`
	Contact       string `yaml:"contact" json:"contact"`
	TermsUrl      string `yaml:"terms_url" json:"terms_url"`
	PrivacyUrl    string `yaml:"privacy_url" json:"privacy_url"`
	Cost          string `yaml:"cost" json:"cost"`                     // "free" or "paid"
	MembershipUrl string `yaml:"membership_url" json:"membership_url"` // signup / pricing link when paid
	// ProfileUrl is where a pubkey links to, with {npub} or {hex} replaced,
	// for example https://wheat.oslim.dev/p/{npub}. Empty means njump.
	ProfileUrl string `yaml:"profile_url" json:"profile_url"`
}

// DefaultProfileUrl is used when no profile_url is configured.
const DefaultProfileUrl = "https://njump.me/{npub}"

// ProfileURL expands the configured profile link template for a pubkey.
func (c *Config) ProfileURL(npub, hex string) string {
	tpl := strings.TrimSpace(c.Server.ProfileUrl)
	if tpl == "" {
		tpl = DefaultProfileUrl
	}
	out := strings.ReplaceAll(tpl, "{npub}", npub)
	return strings.ReplaceAll(out, "{hex}", hex)
}

type Config struct {
	DbPath                   string              `yaml:"db_path" json:"db_path"`
	LogLevel                 string              `yaml:"log_level" json:"log_level"`
	ApiAddr                  string              `yaml:"api_addr" json:"api_addr"`
	CdnUrl                   string              `yaml:"cdn_url" json:"cdn_url"`
	AdminPubkey              string              `yaml:"admin_pubkey" json:"admin_pubkey"`
	NostrUsersUrl            string              `yaml:"nostr_users_url" json:"nostr_users_url"`
	MaxUploadSizeBytes       int                 `yaml:"max_upload_size_bytes" json:"max_upload_size_bytes"`
	MaxStoragePerPubkeyBytes int64               `yaml:"max_storage_per_pubkey_bytes" json:"max_storage_per_pubkey_bytes"`
	AccessControlRules       []AccessControlRule `yaml:"access_control_rules" json:"access_control_rules"`
	AllowedMimeTypes         []string            `yaml:"allowed_mime_types" json:"allowed_mime_types"`
	ZeroXZero                ZeroXZeroConfig     `yaml:"zero_x_zero,omitempty" json:"zero_x_zero"`

	// Server describes this deployment to visitors and clients.
	Server ServerInfo `yaml:"server" json:"server"`
	// IndexRelays override grain's built-in seed relays used to resolve
	// profiles and relay lists for any pubkey.
	IndexRelays []string `yaml:"index_relays,omitempty" json:"index_relays"`
	// PublicListing controls the unauthenticated all-blobs listing used by
	// gallery-style frontends. Nil means enabled, so existing deployments
	// keep working; drive deployments set it to false.
	PublicListing *bool `yaml:"public_listing" json:"public_listing"`
}

// PublicListingEnabled reports whether /list-all is served.
func (c *Config) PublicListingEnabled() bool {
	return c.PublicListing == nil || *c.PublicListing
}

// Retention is the human label for how long blobs live here.
func (c *Config) Retention() string {
	if c.ZeroXZero.Enabled {
		return "ephemeral"
	}
	return "permanent"
}

// Claimed reports whether an operator has been set.
func (c *Config) Claimed() bool { return c.AdminPubkey != "" }

// IsAdmin reports whether pubkey is the operator.
func (c *Config) IsAdmin(pubkey string) bool {
	return c.AdminPubkey != "" && strings.EqualFold(c.AdminPubkey, pubkey)
}

// DisplayName is the server name with a fallback.
func (c *Config) DisplayName() string {
	if c.Server.Name != "" {
		return c.Server.Name
	}
	return "Lotus"
}

// Validate rejects values the rest of the server cannot work with.
func (c *Config) Validate() error {
	if c.AdminPubkey != "" && !isHex64(c.AdminPubkey) {
		return errors.New("admin_pubkey must be a 64-character hex pubkey")
	}
	switch strings.ToLower(c.Server.Cost) {
	case "", "free", "paid":
	default:
		return errors.New("server.cost must be \"free\" or \"paid\"")
	}
	if p := strings.TrimSpace(c.Server.ProfileUrl); p != "" && !strings.Contains(p, "{npub}") && !strings.Contains(p, "{hex}") {
		return errors.New("server.profile_url must contain {npub} or {hex}, for example https://example.com/p/{npub}")
	}
	if c.MaxUploadSizeBytes < 0 {
		return errors.New("max_upload_size_bytes cannot be negative")
	}
	if c.MaxStoragePerPubkeyBytes < 0 {
		return errors.New("max_storage_per_pubkey_bytes cannot be negative")
	}
	for i, r := range c.AccessControlRules {
		action := strings.ToUpper(r.Action)
		if action != "ALLOW" && action != "DENY" {
			return fmt.Errorf("access rule %d: action must be ALLOW or DENY", i+1)
		}
		switch strings.ToUpper(r.Resource) {
		case "UPLOAD", "GET", "DELETE", "LIST", "MIRROR":
		default:
			return fmt.Errorf("access rule %d: unknown resource %q", i+1, r.Resource)
		}
		if r.Pubkey != "ALL" && !isHex64(r.Pubkey) {
			return fmt.Errorf("access rule %d: pubkey must be ALL or a 64-character hex pubkey", i+1)
		}
	}
	if len(c.AllowedMimeTypes) == 0 {
		return errors.New("allowed_mime_types needs at least one entry (\"*\" for any)")
	}
	return nil
}

// Normalize lowercases keys and uppercases rule words so comparisons are
// exact everywhere else.
func (c *Config) Normalize() {
	c.AdminPubkey = strings.ToLower(strings.TrimSpace(c.AdminPubkey))
	c.Server.Cost = strings.ToLower(strings.TrimSpace(c.Server.Cost))
	for i := range c.AccessControlRules {
		r := &c.AccessControlRules[i]
		r.Action = strings.ToUpper(strings.TrimSpace(r.Action))
		r.Resource = strings.ToUpper(strings.TrimSpace(r.Resource))
		r.Pubkey = strings.TrimSpace(r.Pubkey)
		if strings.EqualFold(r.Pubkey, "ALL") {
			r.Pubkey = "ALL"
		} else {
			r.Pubkey = strings.ToLower(r.Pubkey)
		}
	}
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// NewConfig reads a config file once. The server uses Load; this remains for
// callers that only need a snapshot.
func NewConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := yaml.Unmarshal(raw, cfg); err != nil {
		return nil, err
	}
	cfg.Normalize()
	return cfg, nil
}

// Store owns the running configuration and its file.
type Store struct {
	mu    sync.RWMutex
	path  string
	cfg   Config
	apply func(Config) error
}

// ErrAlreadyClaimed is returned by Claim when an operator is already set.
var ErrAlreadyClaimed = errors.New("this server already has an operator")

// Load reads path, or writes the embedded default there first when the file
// does not exist. The second result reports whether a default was written.
func Load(path string) (*Store, bool, error) {
	created := false
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, false, err
		}
		if err := os.WriteFile(path, DefaultYAML, 0o644); err != nil {
			return nil, false, err
		}
		created = true
	}
	cfg, err := NewConfig(path)
	if err != nil {
		return nil, created, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, created, fmt.Errorf("%s: %w", path, err)
	}
	return &Store{path: path, cfg: *cfg}, created, nil
}

// Get returns a snapshot of the current configuration.
func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// Path is the file the store reads and writes.
func (s *Store) Path() string { return s.path }

// SetApplyHook registers the function that pushes a candidate configuration
// into the running services. It runs before the file is written; an error
// rejects the update and the previous configuration is re-applied.
func (s *Store) SetApplyHook(fn func(Config) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apply = fn
}

// Update applies mutate to a copy of the configuration, validates it, lets
// the services adopt it, writes the file, and publishes it.
func (s *Store) Update(mutate func(*Config) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	next := s.cfg
	next.AccessControlRules = append([]AccessControlRule(nil), s.cfg.AccessControlRules...)
	next.AllowedMimeTypes = append([]string(nil), s.cfg.AllowedMimeTypes...)
	next.IndexRelays = append([]string(nil), s.cfg.IndexRelays...)
	if s.cfg.PublicListing != nil {
		v := *s.cfg.PublicListing
		next.PublicListing = &v
	}

	if err := mutate(&next); err != nil {
		return err
	}
	next.Normalize()
	if err := next.Validate(); err != nil {
		return err
	}
	if s.apply != nil {
		if err := s.apply(next); err != nil {
			_ = s.apply(s.cfg)
			return err
		}
	}
	if err := writeAtomic(s.path, &next); err != nil {
		if s.apply != nil {
			_ = s.apply(s.cfg)
		}
		return err
	}
	s.cfg = next
	return nil
}

// Claim sets the operator if none is set, and grants them upload access.
func (s *Store) Claim(pubkey string) error {
	return s.Update(func(c *Config) error {
		if c.AdminPubkey != "" {
			return ErrAlreadyClaimed
		}
		pubkey = strings.ToLower(pubkey)
		c.AdminPubkey = pubkey
		for _, r := range c.AccessControlRules {
			if r.Pubkey == pubkey && strings.EqualFold(r.Resource, "UPLOAD") {
				return nil
			}
		}
		c.AccessControlRules = append(c.AccessControlRules, AccessControlRule{Action: "ALLOW", Pubkey: pubkey, Resource: "UPLOAD"})
		return nil
	})
}

func writeAtomic(path string, cfg *Config) error {
	out, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := []byte("# Lotus configuration. Written by the admin page; comments are not preserved.\n# Keys db_path, log_level, api_addr and cdn_url take effect after a restart.\n\n")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(header, out...), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
