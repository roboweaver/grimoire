package main

import (
	"slices"
	"strings"
	"testing"
)

// hasEnv reports whether argv contains the "-e" flag followed by want.
func hasEnv(argv []string, want string) bool {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "-e" && argv[i+1] == want {
			return true
		}
	}
	return false
}

// hasFlagValue reports whether argv contains flag followed by value.
func hasFlagValue(argv []string, flag, value string) bool {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}

// TestPodCreateArgvPorts pins the publishing rules: the DB pod publishes NO
// port (MySQL is wp-net only), the web pod publishes WordPressPort:80, and the
// grimoire pod publishes GrimoirePort:8080.
func TestPodCreateArgvPorts(t *testing.T) {
	cfg := defaultPodmanConfig()

	db := cfg.dbPodCreateArgv()
	if slices.Contains(db, "-p") {
		t.Errorf("db pod must not publish any port; got %v", db)
	}

	web := cfg.webPodCreateArgv()
	if !hasFlagValue(web, "-p", "8090:80") {
		t.Errorf("web pod must publish 8090:80; got %v", web)
	}

	grim := cfg.grimoirePodCreateArgv()
	if !hasFlagValue(grim, "-p", "8080:8080") {
		t.Errorf("grimoire pod must publish 8080:8080; got %v", grim)
	}
}

// TestGrimoireRunArgvHasNoDSNNoPassword guards the secrets-not-env-vars
// invariant: the grimoire container carries NO DSN and NO plaintext DB password
// — the entrypoint composes the DSN from the mounted secret.
func TestGrimoireRunArgvHasNoDSNNoPassword(t *testing.T) {
	cfg := defaultPodmanConfig()
	argv := cfg.grimoireRunArgv()
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "GRIMOIRE_DATABASE_DSN") {
		t.Errorf("grimoire run must NOT set GRIMOIRE_DATABASE_DSN; got %v", argv)
	}
	if strings.Contains(joined, "PASSWORD=") {
		t.Errorf("grimoire run must NOT carry a plaintext password; got %v", argv)
	}
	if !slices.Contains(argv, "--secret") || !slices.Contains(argv, cfg.UserSecret) {
		t.Errorf("grimoire run must mount the %s secret; got %v", cfg.UserSecret, argv)
	}
	if !hasEnv(argv, "GRIMOIRE_DATABASE_TABLE_PREFIX="+cfg.TablePrefix) {
		t.Errorf("grimoire run must pass the table prefix env; got %v", argv)
	}
}

// TestWebRunArgvCrossPodDNSAndPrefix pins cross-pod DNS (wp-db-pod:3306, not
// localhost) and the shared table prefix, and that the password comes from the
// secret file rather than a plaintext value.
func TestWebRunArgvCrossPodDNSAndPrefix(t *testing.T) {
	cfg := defaultPodmanConfig()
	argv := cfg.webRunArgv()
	if !hasEnv(argv, "WORDPRESS_DB_HOST=wp-db-pod:3306") {
		t.Errorf("WORDPRESS_DB_HOST must be wp-db-pod:3306 (cross-pod DNS); got %v", argv)
	}
	if hasEnv(argv, "WORDPRESS_DB_HOST=localhost:3306") || hasEnv(argv, "WORDPRESS_DB_HOST=127.0.0.1:3306") {
		t.Errorf("WORDPRESS_DB_HOST must not be localhost; got %v", argv)
	}
	if !hasEnv(argv, "WORDPRESS_TABLE_PREFIX="+cfg.TablePrefix) {
		t.Errorf("WORDPRESS_TABLE_PREFIX must equal the configured prefix; got %v", argv)
	}
	if !hasEnv(argv, "WORDPRESS_DB_PASSWORD_FILE=/run/secrets/"+cfg.UserSecret) {
		t.Errorf("WordPress must read the password from the secret file; got %v", argv)
	}
	if strings.Contains(strings.Join(argv, " "), "WORDPRESS_DB_PASSWORD=") {
		t.Errorf("WordPress must NOT carry a plaintext DB password; got %v", argv)
	}
}

// TestTablePrefixDefaultNoTrailingUnderscore locks the single most error-prone
// value: the default prefix is "accuweaver" with no trailing underscore, and it
// flows identically to both apps.
func TestTablePrefixDefaultNoTrailingUnderscore(t *testing.T) {
	if TablePrefixDefault != "accuweaver" {
		t.Fatalf("default prefix must be \"accuweaver\"; got %q", TablePrefixDefault)
	}
	if strings.HasSuffix(TablePrefixDefault, "_") {
		t.Fatalf("default prefix must NOT have a trailing underscore; got %q", TablePrefixDefault)
	}
	cfg := defaultPodmanConfig()
	if !hasEnv(cfg.webRunArgv(), "WORDPRESS_TABLE_PREFIX=accuweaver") {
		t.Error("WordPress must receive the accuweaver prefix")
	}
	if !hasEnv(cfg.grimoireRunArgv(), "GRIMOIRE_DATABASE_TABLE_PREFIX=accuweaver") {
		t.Error("grimoire must receive the accuweaver prefix")
	}
}

// TestDBRunArgvSecretsAndHealth pins that MySQL credentials are *_FILE from
// secrets (never plaintext) and that the healthcheck the up gate polls is set.
func TestDBRunArgvSecretsAndHealth(t *testing.T) {
	cfg := defaultPodmanConfig()
	argv := cfg.dbRunArgv()
	if !hasEnv(argv, "MYSQL_ROOT_PASSWORD_FILE=/run/secrets/"+cfg.RootSecret) {
		t.Errorf("MySQL root password must come from the secret file; got %v", argv)
	}
	if !hasEnv(argv, "MYSQL_PASSWORD_FILE=/run/secrets/"+cfg.UserSecret) {
		t.Errorf("MySQL user password must come from the secret file; got %v", argv)
	}
	if strings.Contains(strings.Join(argv, " "), "MYSQL_ROOT_PASSWORD=") {
		t.Errorf("MySQL must not carry a plaintext root password; got %v", argv)
	}
	if !hasFlagValue(argv, "--health-cmd", "mysqladmin ping -h 127.0.0.1") {
		t.Errorf("MySQL must set the health-cmd the up gate polls; got %v", argv)
	}
	if slices.Contains(argv, "-p") {
		t.Errorf("MySQL container must not publish a host port; got %v", argv)
	}
}

// TestMigrateOneShotsUseOverlayAndCheckNeverGreenfield guards the
// overlay-not-greenfield invariant: the migrate one-shots carry -check and
// -overlay, and neither is ever a bare greenfield migrate.
func TestMigrateOneShotsUseOverlayAndCheckNeverGreenfield(t *testing.T) {
	cfg := defaultPodmanConfig()

	check := cfg.migrateCheckArgv()
	if !slices.Contains(check, "migrate") || !slices.Contains(check, "-check") {
		t.Errorf("preflight must be migrate -check; got %v", check)
	}

	overlay := cfg.migrateOverlayArgv()
	if !slices.Contains(overlay, "migrate") || !slices.Contains(overlay, "-overlay") {
		t.Errorf("migration must be migrate -overlay; got %v", overlay)
	}

	// Neither one-shot may be a bare greenfield migrate (migrate with no
	// -overlay / -check): that issues ALTER TABLE against WordPress tables.
	for _, argv := range [][]string{check, overlay} {
		if slices.Contains(argv, "migrate") &&
			!slices.Contains(argv, "-overlay") && !slices.Contains(argv, "-check") {
			t.Errorf("greenfield migrate must never be constructed; got %v", argv)
		}
		// One-shots must be throwaway and on the shared network.
		if !slices.Contains(argv, "--rm") || !hasFlagValue(argv, "--network", cfg.Network) {
			t.Errorf("one-shot must be --rm on the shared network; got %v", argv)
		}
		if !hasEnv(argv, "GRIMOIRE_DATABASE_TABLE_PREFIX="+cfg.TablePrefix) {
			t.Errorf("one-shot must carry the table prefix; got %v", argv)
		}
	}
}

// TestNonDefaultConfigFlowsIntoArgv proves the command is reusable: a custom
// prefix and ports flow through to the generated argv.
func TestNonDefaultConfigFlowsIntoArgv(t *testing.T) {
	cfg := defaultPodmanConfig()
	cfg.TablePrefix = "wp_"
	cfg.GrimoirePort = 9000
	cfg.WordPressPort = 9090

	if !hasEnv(cfg.grimoireRunArgv(), "GRIMOIRE_DATABASE_TABLE_PREFIX=wp_") {
		t.Error("custom prefix must flow into grimoire run argv")
	}
	if !hasEnv(cfg.webRunArgv(), "WORDPRESS_TABLE_PREFIX=wp_") {
		t.Error("custom prefix must flow into web run argv")
	}
	if !hasFlagValue(cfg.grimoirePodCreateArgv(), "-p", "9000:8080") {
		t.Error("custom grimoire port must flow into pod create argv")
	}
	if !hasFlagValue(cfg.webPodCreateArgv(), "-p", "9090:80") {
		t.Error("custom WordPress port must flow into pod create argv")
	}
}

// TestRestartPolicyArgv pins Fix-1: with RestartPolicy="always", the three
// long-lived stack run argv each carry the single-token `--restart=always`,
// placed among the run flags before the image; the migrate one-shots and the
// pod-create argv never carry --restart.
func TestRestartPolicyArgv(t *testing.T) {
	cfg := defaultPodmanConfig()
	cfg.RestartPolicy = "always"

	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"db", cfg.dbRunArgv()},
		{"web", cfg.webRunArgv()},
		{"grimoire", cfg.grimoireRunArgv()},
	} {
		if !slices.Contains(tc.argv, "--restart=always") {
			t.Errorf("%s run argv must contain --restart=always; got %v", tc.name, tc.argv)
		}
		// Single-token form: the two-token "--restart always" must not appear.
		if hasFlagValue(tc.argv, "--restart", "always") {
			t.Errorf("%s run argv must use the single-token --restart=always form; got %v", tc.name, tc.argv)
		}
		// The flag must sit before the image (among the run flags).
		restartIdx := slices.Index(tc.argv, "--restart=always")
		imageIdx := slices.IndexFunc(tc.argv, func(s string) bool {
			return s == cfg.MySQLImage || s == cfg.WordPressImage || s == cfg.GrimoireImage
		})
		if imageIdx >= 0 && restartIdx > imageIdx {
			t.Errorf("%s --restart=always must come before the image; got %v", tc.name, tc.argv)
		}
	}

	// One-shots and pod-create argv must never carry --restart.
	for _, argv := range [][]string{
		cfg.cliOneShotArgv("migrate", "-overlay"),
		cfg.dbPodCreateArgv(), cfg.webPodCreateArgv(), cfg.grimoirePodCreateArgv(),
	} {
		if slices.Contains(argv, "--restart=always") || slices.Contains(argv, "--restart") {
			t.Errorf("one-shot / pod-create argv must not carry --restart; got %v", argv)
		}
	}
}

// TestRestartPolicyEmptyOmitsFlag pins the default (unchanged) behavior: with an
// empty RestartPolicy none of the run argv carry --restart in any form.
func TestRestartPolicyEmptyOmitsFlag(t *testing.T) {
	cfg := defaultPodmanConfig() // RestartPolicy == ""
	for _, tc := range []struct {
		name string
		argv []string
	}{
		{"db", cfg.dbRunArgv()},
		{"web", cfg.webRunArgv()},
		{"grimoire", cfg.grimoireRunArgv()},
	} {
		for _, a := range tc.argv {
			if strings.HasPrefix(a, "--restart") {
				t.Errorf("%s run argv must omit --restart when RestartPolicy is empty; got %v", tc.name, tc.argv)
			}
		}
	}
}

// TestBuildArgv pins the build command shape.
func TestBuildArgv(t *testing.T) {
	cfg := defaultPodmanConfig()
	argv := cfg.buildArgv("/repo/Containerfile", "/repo")
	want := []string{"build", "-t", "localhost/grimoire:dev", "-f", "/repo/Containerfile", "/repo"}
	if !slices.Equal(argv, want) {
		t.Errorf("buildArgv = %v, want %v", argv, want)
	}
}

// TestHealthStatusArgv pins the inspect the up gate polls.
func TestHealthStatusArgv(t *testing.T) {
	cfg := defaultPodmanConfig()
	argv := cfg.healthStatusArgv()
	want := []string{"inspect", "wp-mysql", "--format", "{{.State.Health.Status}}"}
	if !slices.Equal(argv, want) {
		t.Errorf("healthStatusArgv = %v, want %v", argv, want)
	}
}
