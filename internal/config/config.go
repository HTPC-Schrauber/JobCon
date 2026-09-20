package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server      ServerConfig      `yaml:"server"`
	TLS         TLSConfig         `yaml:"tls"`
	Auth        AuthConfig        `yaml:"auth"`
	Database    DatabaseConfig    `yaml:"database"`
	Storage     StorageConfig     `yaml:"storage"`
	Nexus       NexusConfig       `yaml:"nexus"`
	SSHDefaults SSHDefaultsConfig `yaml:"ssh_defaults"`
}

type ServerConfig struct {
	Bind           string   `yaml:"bind"`
	Port           int      `yaml:"port"`
	BaseURL        string   `yaml:"base_url"`
	TrustedProxies []string `yaml:"trusted_proxies"`
}

type TLSConfig struct {
	Enabled  bool   `yaml:"enabled"`
	CertFile string `yaml:"cert_file"`
	KeyFile  string `yaml:"key_file"`
}

type AuthConfig struct {
	SessionSecretEnv string     `yaml:"session_secret_env"`
	SessionSecret    string     `yaml:"session_secret"`
	Mode             string     `yaml:"mode"` // "local" or "ldap"
	AdminPassword    string     `yaml:"-"`    // Read from env JOBCON_ADMIN_PASSWORD
	LDAP             LDAPConfig `yaml:"ldap"`
}

type LDAPConfig struct {
	Enabled            bool              `yaml:"enabled"`
	Host               string            `yaml:"host"`
	Port               int               `yaml:"port"`
	UseSSL             bool              `yaml:"use_ssl"`
	InsecureSkipVerify bool              `yaml:"insecure_skip_verify"`
	BindDN             string            `yaml:"bind_dn"`
	BindPasswordEnv    string            `yaml:"bind_password_env"`
	BindPassword       string            `yaml:"bind_password"`
	BaseDN             string            `yaml:"base_dn"`
	UserFilter         string            `yaml:"user_filter"`
	RoleMappings       map[string]string `yaml:"role_mappings"`
}

type DatabaseConfig struct {
	Path string `yaml:"path"`
}

type StorageConfig struct {
	LogsDir           string `yaml:"logs_dir"`
	CompressCompleted bool   `yaml:"compress_completed"`
}

type NexusRepository struct {
	ID    string `json:"id" yaml:"id"`
	Label string `json:"label" yaml:"label"`
}

type NexusConfig struct {
	BaseURL      string            `json:"base_url" yaml:"base_url"`
	Username     string            `json:"username" yaml:"username"`
	PasswordEnv  string            `json:"password_env" yaml:"password_env"`
	Password     string            `json:"password" yaml:"password"`
	Repositories []NexusRepository `json:"repositories" yaml:"repositories"`
}

type SSHDefaultsConfig struct {
	User                     string `yaml:"user"`
	KeyPath                  string `yaml:"key_path"`
	TimeoutSeconds           int    `yaml:"timeout_seconds"`
	KeepaliveIntervalSeconds int    `yaml:"keepalive_interval_seconds"`
}

// DefaultConfig returns reasonable defaults for Linux deployment
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Bind:           "0.0.0.0",
			Port:           8080,
			BaseURL:        "http://localhost:8080",
			TrustedProxies: []string{"127.0.0.1/32", "::1/128"},
		},
		TLS: TLSConfig{
			Enabled: false,
		},
		Auth: AuthConfig{
			Mode: "local",
			LDAP: LDAPConfig{
				Port:       636,
				UseSSL:     true,
				UserFilter: "(&(objectClass=user)(sAMAccountName=%s))",
			},
		},
		Database: DatabaseConfig{
			Path: "./data/jobcon.db",
		},
		Storage: StorageConfig{
			LogsDir:           "./data/logs",
			CompressCompleted: true,
		},
		Nexus: NexusConfig{
			BaseURL: "https://nexus.intern/repository",
			Repositories: []NexusRepository{
				{ID: "releases", Label: "Releases (Produktion)"},
				{ID: "snapshots", Label: "Snapshots (Entwicklung)"},
			},
		},
		SSHDefaults: SSHDefaultsConfig{
			User:                     "talend",
			KeyPath:                  filepath.Join(os.Getenv("HOME"), ".ssh", "id_ed25519"),
			TimeoutSeconds:           30,
			KeepaliveIntervalSeconds: 30,
		},
	}
}

// LoadConfig loads configuration from a YAML file and overrides with environment variables
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
			}
			// File does not exist: proceed with defaults
		} else {
			if err := yaml.Unmarshal(data, cfg); err != nil {
				return nil, fmt.Errorf("failed to parse yaml config %q: %w", path, err)
			}
		}
	}

	// Environment variable overrides
	if envPort := os.Getenv("JOBCON_PORT"); envPort != "" {
		var p int
		if _, err := fmt.Sscanf(envPort, "%d", &p); err == nil && p > 0 {
			cfg.Server.Port = p
		}
	}
	if envBind := os.Getenv("JOBCON_BIND"); envBind != "" {
		cfg.Server.Bind = envBind
	}
	if envDB := os.Getenv("JOBCON_DB_PATH"); envDB != "" {
		cfg.Database.Path = envDB
	}
	if envLogs := os.Getenv("JOBCON_LOGS_DIR"); envLogs != "" {
		cfg.Storage.LogsDir = envLogs
	}
	if envNexusPass := os.Getenv("JOBCON_NEXUS_PASSWORD"); envNexusPass != "" {
		cfg.Nexus.Password = envNexusPass
	} else if cfg.Nexus.PasswordEnv != "" {
		cfg.Nexus.Password = os.Getenv(cfg.Nexus.PasswordEnv)
	}

	if envLDAPPass := os.Getenv("JOBCON_LDAP_PASSWORD"); envLDAPPass != "" {
		cfg.Auth.LDAP.BindPassword = envLDAPPass
	} else if cfg.Auth.LDAP.BindPasswordEnv != "" {
		cfg.Auth.LDAP.BindPassword = os.Getenv(cfg.Auth.LDAP.BindPasswordEnv)
	}

	// Session secret resolution
	if envSecret := os.Getenv("JOBCON_SESSION_SECRET"); envSecret != "" {
		cfg.Auth.SessionSecret = envSecret
	} else if cfg.Auth.SessionSecretEnv != "" {
		cfg.Auth.SessionSecret = os.Getenv(cfg.Auth.SessionSecretEnv)
	}
	if cfg.Auth.SessionSecret == "" {
		// Generate random 32-byte secret if not provided
		randomBytes := make([]byte, 32)
		if _, err := rand.Read(randomBytes); err == nil {
			cfg.Auth.SessionSecret = hex.EncodeToString(randomBytes)
		} else {
			cfg.Auth.SessionSecret = "jobcon-insecure-default-secret-change-me"
		}
	}

	// Admin initial password from env
	cfg.Auth.AdminPassword = os.Getenv("JOBCON_ADMIN_PASSWORD")

	// Ensure directories exist
	if err := os.MkdirAll(filepath.Dir(cfg.Database.Path), 0750); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}
	if err := os.MkdirAll(cfg.Storage.LogsDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Normalize base url
	cfg.Server.BaseURL = strings.TrimRight(cfg.Server.BaseURL, "/")

	return cfg, nil
}
