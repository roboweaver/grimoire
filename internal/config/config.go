package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SupportedVendors lists the database backends grimoire can talk to.
var SupportedVendors = []string{"mysql", "postgres", "sqlite"}

// Config is the top-level grimoire configuration.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Theme    string         `yaml:"theme"`
	Database DatabaseConfig `yaml:"database"`
	Session  SessionConfig  `yaml:"session"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Addr string `yaml:"addr"`
}

// SessionConfig configures authenticated session cookies. No signing secret is
// needed: the cookie holds an opaque random token and the server stores only
// its hash (see internal/auth.SessionManager).
type SessionConfig struct {
	// CookieName is the session cookie name (default "grimoire_session").
	CookieName string `yaml:"cookie_name"`
	// CookieSecure sets the Secure attribute; enable when serving over TLS.
	CookieSecure bool `yaml:"cookie_secure"`
	// TTLHours is the rolling session lifetime in hours (default 336 = 14 days).
	TTLHours int `yaml:"ttl_hours"`
}

// TTL returns the rolling session lifetime as a time.Duration.
func (s SessionConfig) TTL() time.Duration {
	return time.Duration(s.TTLHours) * time.Hour
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
	if v, ok := os.LookupEnv("GRIMOIRE_SESSION_COOKIE_NAME"); ok {
		c.Session.CookieName = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_SESSION_COOKIE_SECURE"); ok {
		c.Session.CookieSecure = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := os.LookupEnv("GRIMOIRE_SESSION_TTL_HOURS"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.Session.TTLHours = n
		}
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
	if c.Session.CookieName == "" {
		c.Session.CookieName = "grimoire_session"
	}
	if c.Session.TTLHours <= 0 {
		c.Session.TTLHours = 24 * 14
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

// libpqPasswordRE matches the password token in a libpq keyword-style DSN
// (e.g. "host=... password=secret dbname=..."), including single- or
// double-quoted values.
var libpqPasswordRE = regexp.MustCompile(`(?i)(password\s*=\s*)('[^']*'|"[^"]*"|\S+)`)

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
	// libpq keyword DSN: host=... password=secret dbname=...
	if libpqPasswordRE.MatchString(dsn) {
		return libpqPasswordRE.ReplaceAllString(dsn, "${1}***")
	}
	return dsn
}
