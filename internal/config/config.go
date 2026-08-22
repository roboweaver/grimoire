package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// SupportedVendors lists the database backends grimoire can talk to.
var SupportedVendors = []string{"mysql", "postgres", "sqlite"}

// Config is the top-level grimoire configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Theme    string         `yaml:"theme"`
	Database DatabaseConfig `yaml:"database"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// DatabaseConfig selects and configures the storage backend.
type DatabaseConfig struct {
	Vendor      string `yaml:"vendor"`
	DSN         string `yaml:"dsn"`
	TablePrefix string `yaml:"table_prefix"`
}

// Load reads a YAML config file, applies environment overrides and defaults,
// then validates the result.
func Load(path string) (Config, error) {
	var cfg Config
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("config: read %s: %w", path, err)
	}
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("config: parse %s: %w", path, err)
	}

	cfg.applyEnv()
	cfg.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyEnv() {
	if v, ok := os.LookupEnv("GRIMOIRE_SERVER_ADDR"); ok {
		c.Server.Addr = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_THEME"); ok {
		c.Theme = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_DATABASE_VENDOR"); ok {
		c.Database.Vendor = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_DATABASE_DSN"); ok {
		c.Database.DSN = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_DATABASE_TABLE_PREFIX"); ok {
		c.Database.TablePrefix = v
	}
}

func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.Theme == "" {
		c.Theme = "default"
	}
	if c.Database.TablePrefix == "" {
		c.Database.TablePrefix = "wp_"
	}
}

// Validate ensures required fields are present and the vendor is supported.
func (c Config) Validate() error {
	if c.Database.Vendor == "" {
		return fmt.Errorf("config: database.vendor is required (one of %s)", strings.Join(SupportedVendors, ", "))
	}
	if !isSupportedVendor(c.Database.Vendor) {
		return fmt.Errorf("config: unsupported database.vendor %q (supported: %s)", c.Database.Vendor, strings.Join(SupportedVendors, ", "))
	}
	if strings.TrimSpace(c.Database.DSN) == "" {
		return fmt.Errorf("config: database.dsn is required for vendor %q", c.Database.Vendor)
	}
	return nil
}

func isSupportedVendor(v string) bool {
	for _, s := range SupportedVendors {
		if s == v {
			return true
		}
	}
	return false
}

var mysqlCredRE = regexp.MustCompile(`^[^:/@]+:[^@]*@`)

// RedactDSN returns a copy of the DSN safe for logging, with any embedded
// credentials removed.
func RedactDSN(vendor, dsn string) string {
	if dsn == "" {
		return ""
	}
	// URL-style DSNs (postgres://user:pass@host/db, mysql://...).
	if strings.Contains(dsn, "://") {
		if u, err := url.Parse(dsn); err == nil && u.User != nil {
			u.User = url.User("***")
			return u.String()
		}
	}
	// Go MySQL DSN: user:pass@tcp(host:port)/db
	if mysqlCredRE.MatchString(dsn) {
		return mysqlCredRE.ReplaceAllString(dsn, "***@")
	}
	return dsn
}
