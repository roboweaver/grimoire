package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "grimoire.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}

func TestLoadValidSQLiteAppliesDefaults(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.Vendor != "sqlite" {
		t.Errorf("vendor = %q, want sqlite", cfg.Database.Vendor)
	}
	if cfg.Database.TablePrefix != "wp_" {
		t.Errorf("table_prefix = %q, want default wp_", cfg.Database.TablePrefix)
	}
	if cfg.Theme != "default" {
		t.Errorf("theme = %q, want default", cfg.Theme)
	}
	if cfg.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want default :8080", cfg.Server.Addr)
	}
}

func TestLoadMissingVendorErrors(t *testing.T) {
	path := writeTempConfig(t, `
database:
  dsn: "file:grimoire.db"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for missing vendor")
	}
	if !strings.Contains(err.Error(), "vendor") {
		t.Errorf("error %q should mention vendor", err.Error())
	}
}

func TestLoadUnsupportedVendorErrors(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: oracle
  dsn: "x"
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported vendor")
	}
	if !strings.Contains(err.Error(), "oracle") {
		t.Errorf("error %q should name the invalid vendor", err.Error())
	}
}

func TestLoadMissingDSNErrors(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
`)
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for empty dsn")
	}
	if !strings.Contains(err.Error(), "dsn") {
		t.Errorf("error %q should mention dsn", err.Error())
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	path := writeTempConfig(t, `
server:
  addr: ":8080"
theme: default
database:
  vendor: sqlite
  dsn: "file:base.db"
  table_prefix: "wp_"
`)
	t.Setenv("GRIMOIRE_DATABASE_DSN", "file:override.db")
	t.Setenv("GRIMOIRE_SERVER_ADDR", ":9090")
	t.Setenv("GRIMOIRE_THEME", "night")
	t.Setenv("GRIMOIRE_DATABASE_VENDOR", "sqlite")
	t.Setenv("GRIMOIRE_DATABASE_TABLE_PREFIX", "gr_")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Database.DSN != "file:override.db" {
		t.Errorf("dsn = %q, want override", cfg.Database.DSN)
	}
	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr = %q, want :9090", cfg.Server.Addr)
	}
	if cfg.Theme != "night" {
		t.Errorf("theme = %q, want night", cfg.Theme)
	}
	if cfg.Database.TablePrefix != "gr_" {
		t.Errorf("table_prefix = %q, want gr_", cfg.Database.TablePrefix)
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name        string
		vendor      string
		dsn         string
		mustNot     string
		mustContain string
	}{
		{"mysql", "mysql", "user:secretpass@tcp(127.0.0.1:3306)/wordpress?parseTime=true", "secretpass", ""},
		{"postgres-url", "postgres", "postgres://u:topsecret@127.0.0.1:5432/wordpress?sslmode=disable", "topsecret", ""},
		{"postgres-keyword", "postgres", "host=127.0.0.1 user=grim password=secretpass dbname=wordpress sslmode=disable", "secretpass", "password=***"},
		{"postgres-keyword-quoted", "postgres", "host=127.0.0.1 password='se cret' dbname=wp", "se cret", "password=***"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactDSN(tc.vendor, tc.dsn)
			if strings.Contains(got, tc.mustNot) {
				t.Errorf("RedactDSN(%q) = %q, must not contain %q", tc.dsn, got, tc.mustNot)
			}
			if tc.mustContain != "" && !strings.Contains(got, tc.mustContain) {
				t.Errorf("RedactDSN(%q) = %q, must contain %q", tc.dsn, got, tc.mustContain)
			}
		})
	}
}

func TestLoadSessionDefaults(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.CookieName != "grimoire_session" {
		t.Errorf("session.cookie_name = %q, want default grimoire_session", cfg.Session.CookieName)
	}
	if cfg.Session.TTLHours != 24*14 {
		t.Errorf("session.ttl_hours = %d, want default 336", cfg.Session.TTLHours)
	}
	if cfg.Session.CookieSecure {
		t.Error("session.cookie_secure should default to false")
	}
	if got := cfg.Session.TTL(); got != 14*24*time.Hour {
		t.Errorf("Session.TTL() = %s, want 336h", got)
	}
}

func TestLoadSessionExplicitAndEnvOverride(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
session:
  cookie_name: sess
  cookie_secure: true
  ttl_hours: 48
`)
	t.Setenv("GRIMOIRE_SESSION_COOKIE_NAME", "envsess")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Session.CookieName != "envsess" {
		t.Errorf("cookie_name = %q, want env override envsess", cfg.Session.CookieName)
	}
	if !cfg.Session.CookieSecure {
		t.Error("cookie_secure should be true from file")
	}
	if cfg.Session.TTLHours != 48 {
		t.Errorf("ttl_hours = %d, want 48", cfg.Session.TTLHours)
	}
}

func TestLoadMediaDefaults(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Media.UploadsDir != "wp-content/uploads" {
		t.Fatalf("uploads_dir=%q", cfg.Media.UploadsDir)
	}
	if cfg.Media.MaxUploadSize != 10<<20 {
		t.Fatalf("max_upload_size=%d", cfg.Media.MaxUploadSize)
	}
}

func TestLoadRESTDefaults(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.REST.RequireTLSForApplicationPasswords {
		t.Error("rest.require_tls_for_application_passwords should default to true when omitted from the config file")
	}
	if cfg.REST.TrustedProxyHeader != "" {
		t.Errorf("rest.trusted_proxy_header = %q, want empty default", cfg.REST.TrustedProxyHeader)
	}
	if cfg.REST.PerPageMax != 100 {
		t.Errorf("rest.per_page_max = %d, want default 100", cfg.REST.PerPageMax)
	}
}

func TestLoadRESTExplicitFalseIsHonored(t *testing.T) {
	// Regression guard: RequireTLSForApplicationPasswords is pre-set to
	// true before yaml.Unmarshal so an omitted key keeps the secure
	// default (TestLoadRESTDefaults above); this test confirms an
	// explicit "false" in the file is not clobbered by that pre-set.
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
rest:
  require_tls_for_application_passwords: false
  trusted_proxy_header: "X-Forwarded-Proto"
  per_page_max: 25
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.REST.RequireTLSForApplicationPasswords {
		t.Error("explicit require_tls_for_application_passwords: false should be honored, not overridden by the pre-set default")
	}
	if cfg.REST.TrustedProxyHeader != "X-Forwarded-Proto" {
		t.Errorf("trusted_proxy_header = %q, want X-Forwarded-Proto", cfg.REST.TrustedProxyHeader)
	}
	if cfg.REST.PerPageMax != 25 {
		t.Errorf("per_page_max = %d, want 25", cfg.REST.PerPageMax)
	}
}

func TestLoadRESTEnvOverrides(t *testing.T) {
	path := writeTempConfig(t, `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`)
	t.Setenv("GRIMOIRE_REST_REQUIRE_TLS_FOR_APPLICATION_PASSWORDS", "false")
	t.Setenv("GRIMOIRE_REST_TRUSTED_PROXY_HEADER", "X-Forwarded-Proto")
	t.Setenv("GRIMOIRE_REST_PER_PAGE_MAX", "10")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.REST.RequireTLSForApplicationPasswords {
		t.Error("env override should set require_tls_for_application_passwords to false")
	}
	if cfg.REST.TrustedProxyHeader != "X-Forwarded-Proto" {
		t.Errorf("trusted_proxy_header = %q, want env override", cfg.REST.TrustedProxyHeader)
	}
	if cfg.REST.PerPageMax != 10 {
		t.Errorf("per_page_max = %d, want env override 10", cfg.REST.PerPageMax)
	}
}
