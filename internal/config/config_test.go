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

func TestCheckUploadsDirExists(t *testing.T) {
	dir := t.TempDir()
	if err := CheckUploadsDir(dir); err != nil {
		t.Fatalf("CheckUploadsDir(%q) = %v, want nil", dir, err)
	}
}

func TestCheckUploadsDirMissing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	err := CheckUploadsDir(dir)
	if err == nil {
		t.Fatal("CheckUploadsDir on a missing directory: want error, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %q, want mention of 'does not exist'", err.Error())
	}
}

func TestCheckUploadsDirEmpty(t *testing.T) {
	if err := CheckUploadsDir(""); err == nil {
		t.Fatal("CheckUploadsDir(\"\"): want error, got nil")
	}
}

func TestCheckUploadsDirNotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	err := CheckUploadsDir(file)
	if err == nil {
		t.Fatal("CheckUploadsDir on a file path: want error, got nil")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error = %q, want mention of 'not a directory'", err.Error())
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

// TestShippedConfigsLoadAndDocumentUploadsDir guards the shipped example
// configs in configs/*.yaml: they must remain loadable, and must each
// discoverably document media.uploads_dir (not just rely on the code-level
// default), since operators pointing grimoire at an existing external
// WordPress database need to find and set this field in the file they are
// already editing rather than discover it only via CheckUploadsDir's
// runtime warning or the README.
func TestShippedConfigsLoadAndDocumentUploadsDir(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.yaml"))
	if err != nil {
		t.Fatalf("glob configs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped configs found under configs/*.yaml")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			if !strings.Contains(string(raw), "uploads_dir:") {
				t.Fatalf("%s does not document media.uploads_dir; add a discoverable uploads_dir example under a media: block", path)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load(%s): %v", path, err)
			}
			if cfg.Media.UploadsDir == "" {
				t.Fatalf("%s: Media.UploadsDir is empty after Load", path)
			}
		})
	}
}

// --- content.trash_days (M22, Req 20.1-20.6; D6) ---

const trashDaysBase = `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`

func loadTrashDays(t *testing.T, contentBlock string) (Config, error) {
	t.Helper()
	return Load(writeTempConfig(t, trashDaysBase+contentBlock))
}

func TestLoadTrashDaysDefaultsTo30(t *testing.T) {
	cfg, err := loadTrashDays(t, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Content.EffectiveTrashDays(); got != 30 {
		t.Errorf("EffectiveTrashDays() = %d, want default 30", got)
	}
	if !cfg.Content.TrashEnabled() {
		t.Error("TrashEnabled() = false, want true for the default")
	}
}

func TestLoadTrashDaysZeroIsNotDefaulted(t *testing.T) {
	cfg, err := loadTrashDays(t, "content:\n  trash_days: 0\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Content.TrashDays == nil || *cfg.Content.TrashDays != 0 {
		t.Fatalf("TrashDays = %v, want pointer to 0", cfg.Content.TrashDays)
	}
	if got := cfg.Content.EffectiveTrashDays(); got != 0 {
		t.Errorf("EffectiveTrashDays() = %d, want 0", got)
	}
	if cfg.Content.TrashEnabled() {
		t.Error("TrashEnabled() = true, want false when trash_days is 0")
	}
}

func TestLoadTrashDaysExplicit(t *testing.T) {
	cfg, err := loadTrashDays(t, "content:\n  trash_days: 7\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Content.EffectiveTrashDays(); got != 7 {
		t.Errorf("EffectiveTrashDays() = %d, want 7", got)
	}
	if !cfg.Content.TrashEnabled() {
		t.Error("TrashEnabled() = false, want true")
	}
}

func TestLoadTrashDaysInvalidValuesFail(t *testing.T) {
	for _, v := range []string{"-1", "abc", "1.5"} {
		v := v
		t.Run(v, func(t *testing.T) {
			_, err := loadTrashDays(t, "content:\n  trash_days: "+v+"\n")
			if err == nil {
				t.Fatalf("Load with trash_days %s: want error, got nil", v)
			}
			if !strings.Contains(err.Error(), "content.trash_days") {
				t.Errorf("error %q does not name content.trash_days", err)
			}
		})
	}
}

func TestLoadContentUnknownKeyStillFails(t *testing.T) {
	_, err := loadTrashDays(t, "content:\n  trash_dayz: 7\n")
	if err == nil {
		t.Fatal("Load with unknown content key: want error, got nil")
	}
	if !strings.Contains(err.Error(), "trash_dayz") {
		t.Errorf("error %q does not name the unknown key", err)
	}
}

func TestLoadTrashDaysEnvOverridesFile(t *testing.T) {
	t.Setenv("GRIMOIRE_CONTENT_TRASH_DAYS", "14")
	cfg, err := loadTrashDays(t, "content:\n  trash_days: 7\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Content.EffectiveTrashDays(); got != 14 {
		t.Errorf("EffectiveTrashDays() = %d, want env override 14", got)
	}
}

func TestLoadTrashDaysEnvUnparsableIsIgnored(t *testing.T) {
	t.Setenv("GRIMOIRE_CONTENT_TRASH_DAYS", "soon")
	cfg, err := loadTrashDays(t, "content:\n  trash_days: 7\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Content.EffectiveTrashDays(); got != 7 {
		t.Errorf("EffectiveTrashDays() = %d, want file value 7 when env is unparsable", got)
	}
}

func TestContentConfigTrashHelpers(t *testing.T) {
	seven, zero := 7, 0
	cases := []struct {
		name    string
		days    *int
		want    int
		enabled bool
	}{
		{"nil is the default 30", nil, 30, true},
		{"zero disables", &zero, 0, false},
		{"seven", &seven, 7, true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			c := ContentConfig{TrashDays: tc.days}
			if got := c.EffectiveTrashDays(); got != tc.want {
				t.Errorf("EffectiveTrashDays() = %d, want %d", got, tc.want)
			}
			if got := c.TrashEnabled(); got != tc.enabled {
				t.Errorf("TrashEnabled() = %v, want %v", got, tc.enabled)
			}
		})
	}
}

// TestShippedConfigsDocumentTrashDays requires every file under configs/ to
// load and to mention content.trash_days in a comment.
func TestShippedConfigsDocumentTrashDays(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "configs", "*.yaml"))
	if err != nil {
		t.Fatalf("glob configs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no shipped configs found under configs/*.yaml")
	}
	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			documented := false
			for _, line := range strings.Split(string(raw), "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, "trash_days") {
					documented = true
					break
				}
			}
			if !documented {
				t.Errorf("%s has no comment documenting trash_days", path)
			}
			if _, err := Load(path); err != nil {
				t.Fatalf("Load(%s): %v", path, err)
			}
		})
	}
}
