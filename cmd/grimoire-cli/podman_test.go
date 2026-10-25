package main

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
)

// TestRunPodmanUnknownSubcommand proves an unknown subcommand errors.
func TestRunPodmanUnknownSubcommand(t *testing.T) {
	err := runPodman([]string{"frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown-subcommand error; got %v", err)
	}
}

// TestRunPodmanNoSubcommand proves a missing subcommand errors.
func TestRunPodmanNoSubcommand(t *testing.T) {
	if err := runPodman(nil); err == nil {
		t.Fatal("expected error when no subcommand is given")
	}
}

// TestAutostartHelpPaths proves Fix 2: `autostart -h`, `--help`, `help`, and a
// bare `autostart` print the subcommand usage and exit 0 (no error), while a
// genuinely unknown action still errors. None of these paths reach a live
// podman, so the test needs no runner.
func TestAutostartHelpPaths(t *testing.T) {
	for _, args := range [][]string{
		{"autostart", "-h"},
		{"autostart", "--help"},
		{"autostart", "help"},
		{"autostart"},
	} {
		if err := runPodman(args); err != nil {
			t.Errorf("runPodman(%v) must print usage and return nil; got %v", args, err)
		}
	}

	err := runPodman([]string{"autostart", "frobnicate"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected unknown-action error for a bogus action; got %v", err)
	}
}

// TestFlagPrecedenceDefaultEnvFlag proves the resolution order flag > env >
// default on the table prefix and grimoire port.
func TestFlagPrecedenceDefaultEnvFlag(t *testing.T) {
	// default only
	cfg := defaultPodmanConfig()
	cfg.applyEnv(func(string) string { return "" })
	if cfg.TablePrefix != "accuweaver" || cfg.GrimoirePort != 8080 {
		t.Fatalf("defaults wrong: %q %d", cfg.TablePrefix, cfg.GrimoirePort)
	}

	// env overrides default
	env := map[string]string{
		"GRIMOIRE_PODMAN_TABLE_PREFIX":  "envprefix",
		"GRIMOIRE_PODMAN_GRIMOIRE_PORT": "7000",
	}
	cfg = defaultPodmanConfig()
	cfg.applyEnv(func(k string) string { return env[k] })
	if cfg.TablePrefix != "envprefix" || cfg.GrimoirePort != 7000 {
		t.Fatalf("env override failed: %q %d", cfg.TablePrefix, cfg.GrimoirePort)
	}

	// flag overrides env
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	cfg.addFlags(fs)
	if err := fs.Parse([]string{"-table-prefix=flagprefix", "-grimoire-port=9999"}); err != nil {
		t.Fatal(err)
	}
	if cfg.TablePrefix != "flagprefix" || cfg.GrimoirePort != 9999 {
		t.Fatalf("flag override failed: %q %d", cfg.TablePrefix, cfg.GrimoirePort)
	}
}

// TestApplyEnvIgnoresMalformedPort proves a non-numeric port env is ignored
// rather than zeroing the port.
func TestApplyEnvIgnoresMalformedPort(t *testing.T) {
	cfg := defaultPodmanConfig()
	cfg.applyEnv(func(k string) string {
		if k == "GRIMOIRE_PODMAN_GRIMOIRE_PORT" {
			return "not-a-number"
		}
		return ""
	})
	if cfg.GrimoirePort != 8080 {
		t.Errorf("malformed port env must be ignored; got %d", cfg.GrimoirePort)
	}
}

// TestUpEmitsSharedFateAndInvariants drives podmanUp with a fake Runner and
// asserts the shared-fate warning and all five invariant strings are printed —
// the developer guidance is surfaced at the point of use.
func TestUpEmitsSharedFateAndInvariants(t *testing.T) {
	f := newFakeRunner()
	f.existsOK["wp-db-password"] = true
	f.existsOK["wp-db-root-password"] = true
	f.healthSequence = []string{"healthy"}

	var out bytes.Buffer
	if err := podmanUp(context.Background(), f, &out, defaultPodmanConfig(), fastUpOptions()); err != nil {
		t.Fatalf("podmanUp error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		"SHARED-FATE WARNING",
		"NO separator",     // prefix invariant
		"/run/secrets",     // secrets invariant
		"migrate -overlay", // overlay invariant
		"DB health",        // health ordering invariant
		"Cross-pod DNS",    // DNS invariant
		"wp-db-pod:3306",   // the concrete cross-pod address
	} {
		if !strings.Contains(got, want) {
			t.Errorf("up output missing guidance %q\n---\n%s", want, got)
		}
	}
}

// TestStatusProbesAllEndpoints drives podmanStatus with a fake prober and
// asserts all three key endpoints are probed with the right paths and ports.
func TestStatusProbesAllEndpoints(t *testing.T) {
	f := newFakeRunner()
	var probed []string
	fakeProbe := func(_ context.Context, url string) string {
		probed = append(probed, url)
		return "200"
	}
	var out bytes.Buffer
	if err := podmanStatus(context.Background(), f, fakeProbe, &out, defaultPodmanConfig()); err != nil {
		t.Fatalf("podmanStatus error: %v", err)
	}
	for _, want := range []string{
		"http://localhost:8080/healthz",
		"http://localhost:8080/wp-json/wp/v2/posts",
		"http://localhost:8090/",
	} {
		if !containsStr(probed, want) {
			t.Errorf("status must probe %q; probed %v", want, probed)
		}
	}
}

func containsStr(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestDownPreservesData proves down removes exactly the three pods and nothing
// else (no volume/secret/network removal), so data survives.
func TestDownPreservesData(t *testing.T) {
	f := newFakeRunner()
	var out bytes.Buffer
	cfg := defaultPodmanConfig()
	if err := podmanDown(context.Background(), f, &out, cfg); err != nil {
		t.Fatalf("podmanDown error: %v", err)
	}
	podRms := 0
	for _, call := range f.runCalls {
		if len(call) >= 3 && call[1] == "pod" && call[2] == "rm" {
			podRms++
		}
		if len(call) >= 2 && (call[1] == "volume" || call[1] == "secret" || call[1] == "network") {
			t.Errorf("down must not touch volumes/secrets/network; got %v", call)
		}
	}
	if podRms != 3 {
		t.Errorf("down must remove exactly 3 pods; got %d", podRms)
	}
	if !strings.Contains(out.String(), "data survives") {
		t.Error("down should reassure the user that data survives")
	}
}
