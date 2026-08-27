package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
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
	Server    ServerConfig    `yaml:"server"`
	Theme     string          `yaml:"theme"`
	Database  DatabaseConfig  `yaml:"database"`
	Session   SessionConfig   `yaml:"session"`
	Media     MediaConfig     `yaml:"media"`
	REST      RESTConfig      `yaml:"rest"`
	Scheduler SchedulerConfig `yaml:"scheduler"`
}

type MediaConfig struct {
	// UploadsDir is where uploaded media files are read from/written to,
	// resolved relative to the process's working directory if not absolute.
	// Defaults to "wp-content/uploads" (see applyDefaults). When pointing
	// grimoire at an existing external WordPress database, this must be set
	// explicitly to that site's real uploads directory on disk, or
	// /wp-content/uploads/* requests will 404 even though the DB rows for
	// those media items resolve fine.
	UploadsDir    string   `yaml:"uploads_dir"`
	MaxUploadSize int64    `yaml:"max_upload_size"`
	AllowedMIMEs  []string `yaml:"allowed_mime_types"`
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

// RESTConfig configures the /wp-json/wp/v2 REST API surface (Req 8, 10, 12).
type RESTConfig struct {
	// RequireTLSForApplicationPasswords gates HTTP Basic Application
	// Password authentication behind a TLS (or loopback) transport check,
	// matching real WordPress's default refusal to accept Application
	// Passwords over a plain, non-local connection (Req 8.9). Defaults to
	// true; set false only for local development or when TLS is verified
	// to terminate elsewhere in a way this process cannot observe.
	// Note: this defaults to true (see Load, which pre-populates this
	// field before unmarshaling), unlike every other bool in this
	// config which defaults to false.
	RequireTLSForApplicationPasswords bool `yaml:"require_tls_for_application_passwords"`
	// TrustedProxyHeader, if non-empty, names a request header (e.g.
	// "X-Forwarded-Proto") that this operator has configured their
	// TLS-terminating reverse proxy to set reliably; its presence with a
	// value of "https" is treated as an additional "request arrived over
	// TLS" signal for the Application Password transport check. This is
	// an operator-declared-trust setting (same posture as
	// SessionConfig.CookieSecure) and must only be enabled behind a proxy
	// that strips/overwrites this header from untrusted clients.
	TrustedProxyHeader string `yaml:"trusted_proxy_header"`
	// PerPageMax caps the REST "per_page" query parameter (default 100,
	// matching WordPress core's own ceiling).
	PerPageMax int `yaml:"per_page_max"`
}

// DatabaseConfig selects and configures the storage backend.
type DatabaseConfig struct {
	Vendor      string `yaml:"vendor"`
	DSN         string `yaml:"dsn"`
	TablePrefix string `yaml:"table_prefix"`
}

// SchedulerConfig configures the publish scheduler (Requirement 4) that
// polls for "future" posts whose post_date has passed and flips them to
// "publish".
type SchedulerConfig struct {
	// IntervalSeconds is how often the scheduler polls for due posts
	// (default 60, Req 4.2).
	IntervalSeconds int `yaml:"interval_seconds"`
}

// Interval returns the scheduler poll interval as a time.Duration.
func (s SchedulerConfig) Interval() time.Duration {
	return time.Duration(s.IntervalSeconds) * time.Second
}

// Load reads a YAML config file, applies environment overrides and defaults,
// then validates the result.
func Load(path string) (Config, error) {
	var cfg Config
	// RequireTLSForApplicationPasswords defaults to true, unlike every
	// other bool in this config (which default to false). Since
	// yaml.Unmarshal only overwrites fields actually present in the
	// document, pre-setting it here means a config file that simply
	// omits "rest.require_tls_for_application_passwords" keeps the
	// secure default, while a document that explicitly sets it to
	// false is still honored.
	cfg.REST.RequireTLSForApplicationPasswords = true
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
	if v, ok := os.LookupEnv("GRIMOIRE_REST_REQUIRE_TLS_FOR_APPLICATION_PASSWORDS"); ok {
		c.REST.RequireTLSForApplicationPasswords = v == "1" || strings.EqualFold(v, "true")
	}
	if v, ok := os.LookupEnv("GRIMOIRE_REST_TRUSTED_PROXY_HEADER"); ok {
		c.REST.TrustedProxyHeader = v
	}
	if v, ok := os.LookupEnv("GRIMOIRE_REST_PER_PAGE_MAX"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.REST.PerPageMax = n
		}
	}
	if v, ok := os.LookupEnv("GRIMOIRE_SCHEDULER_INTERVAL_SECONDS"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			c.Scheduler.IntervalSeconds = n
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
	if c.Media.UploadsDir == "" {
		c.Media.UploadsDir = "wp-content/uploads"
	}
	if c.Media.MaxUploadSize <= 0 {
		c.Media.MaxUploadSize = 10 << 20
	}
	if len(c.Media.AllowedMIMEs) == 0 {
		c.Media.AllowedMIMEs = []string{"image/png", "image/jpeg", "image/gif", "text/plain"}
	}
	if c.REST.PerPageMax <= 0 {
		c.REST.PerPageMax = 100
	}
	if c.Scheduler.IntervalSeconds <= 0 {
		c.Scheduler.IntervalSeconds = 60
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

// CheckUploadsDir reports whether dir exists and is a directory. Relative
// paths are resolved against the process's current working directory,
// matching how internal/content.MediaService and the /wp-content/uploads/*
// static handler (internal/web/uploads.go) resolve MediaConfig.UploadsDir.
//
// A non-nil result is intentionally not treated as fatal by callers: a
// fresh install may not have any uploads yet. It exists so operators
// pointing grimoire at an existing external WordPress database (where
// media.uploads_dir must be set explicitly, see MediaConfig.UploadsDir) get
// a clear startup diagnostic instead of silent 404s on every image.
func CheckUploadsDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("uploads_dir is empty")
	}
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			abs, absErr := filepath.Abs(dir)
			if absErr != nil {
				abs = dir
			}
			return fmt.Errorf("uploads_dir %q does not exist (resolved to %q); set media.uploads_dir to the site's real WordPress uploads directory or /wp-content/uploads/* requests will 404", dir, abs)
		}
		return fmt.Errorf("uploads_dir %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("uploads_dir %q is not a directory", dir)
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
