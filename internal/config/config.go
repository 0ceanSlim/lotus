package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

type AccessControlRule struct {
	Action   string `yaml:"action"`
	Pubkey   string `yaml:"pubkey"`
	Resource string `yaml:"resource"`
}

type ZeroXZeroConfig struct {
	Enabled          bool    `yaml:"enabled"`
	InstanceUrl      string  `yaml:"instance_url"`
	MaxFileSizeBytes int64   `yaml:"max_file_size_bytes"`
	MinRetentionDays float64 `yaml:"min_retention_days"`
	MaxRetentionDays float64 `yaml:"max_retention_days"`
}

type Config struct {
	DbPath                   string              `yaml:"db_path"`
	LogLevel                 string              `yaml:"log_level"`
	ApiAddr                  string              `yaml:"api_addr"`
	CdnUrl                   string              `yaml:"cdn_url"`
	AdminPubkey              string              `yaml:"admin_pubkey"`
	NostrUsersUrl            string              `yaml:"nostr_users_url"`
	MaxUploadSizeBytes       int                 `yaml:"max_upload_size_bytes"`
	MaxStoragePerPubkeyBytes int64               `yaml:"max_storage_per_pubkey_bytes"`
	AccessControlRules       []AccessControlRule `yaml:"access_control_rules"`
	AllowedMimeTypes         []string            `yaml:"allowed_mime_types"`
	ZeroXZero                ZeroXZeroConfig     `yaml:"zero_x_zero"`
}

func NewConfig(path string) (*Config, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	config := &Config{}
	err = yaml.Unmarshal(bytes, config)

	return config, err
}
