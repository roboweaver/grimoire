package main

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"testing"
)

// TestPlistContent asserts the LaunchAgent plist carries the stable label, the
// `podman machine start <machine>` program arguments, RunAtLoad, and the log
// paths.
func TestPlistContent(t *testing.T) {
	cfg := defaultPodmanConfig()
	out, err := plistContent(cfg, "/opt/homebrew/bin/podman")
	if err != nil {
		t.Fatalf("plistContent error: %v", err)
	}
	for _, want := range []string{
		"<string>com.accuweaver.podman-machine</string>",
		"<string>/opt/homebrew/bin/podman</string>",
		"<string>machine</string>",
		"<string>start</string>",
		"<string>podman-machine-default</string>",
		"<key>RunAtLoad</key>",
		"<true/>",
		"<string>/tmp/podman-machine-autostart.log</string>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plist missing %q\n---\n%s", want, out)
		}
	}
	// Both StandardOut and StandardError paths should be present.
	if strings.Count(out, launchAgentLog) != 2 {
		t.Errorf("expected the log path twice (stdout+stderr); got\n%s", out)
	}
}

// TestPlistContentHonorsCustomMachine proves a non-default machine name flows
// into the plist.
func TestPlistContentHonorsCustomMachine(t *testing.T) {
	cfg := defaultPodmanConfig()
	cfg.Machine = "my-vm"
	out, err := plistContent(cfg, "podman")
	if err != nil {
		t.Fatalf("plistContent error: %v", err)
	}
	if !strings.Contains(out, "<string>my-vm</string>") {
		t.Errorf("custom machine name must appear in plist; got\n%s", out)
	}
}

// TestUpdateRestartArgvInstallUninstall pins Layer A: install uses --restart
// always over exactly the three stack containers; uninstall uses --restart no.
// The runbook's ms365-login is out of scope and must not appear.
func TestUpdateRestartArgvInstallUninstall(t *testing.T) {
	cfg := defaultPodmanConfig()

	install := cfg.updateRestartArgv("always")
	wantInstall := []string{"update", "--restart", "always", "wp-mysql", "wp-wordpress", "grimoire"}
	if !slices.Equal(install, wantInstall) {
		t.Errorf("install argv = %v, want %v", install, wantInstall)
	}
	if slices.Contains(install, "ms365-login") {
		t.Error("ms365-login is out of scope and must not be targeted")
	}

	uninstall := cfg.updateRestartArgv("no")
	if uninstall[2] != "no" {
		t.Errorf("uninstall must use --restart no; got %v", uninstall)
	}
}

// TestMachineSSHSystemctlArgv pins the enable/disable of podman-restart.service
// inside the VM.
func TestMachineSSHSystemctlArgv(t *testing.T) {
	cfg := defaultPodmanConfig()

	enable := cfg.machineSSHSystemctlArgv("enable")
	wantEnable := []string{"machine", "ssh", "podman-machine-default",
		"systemctl --user enable podman-restart.service"}
	if !slices.Equal(enable, wantEnable) {
		t.Errorf("enable argv = %v, want %v", enable, wantEnable)
	}

	disable := cfg.machineSSHSystemctlArgv("disable")
	if !strings.Contains(disable[len(disable)-1], "disable podman-restart.service") {
		t.Errorf("disable argv must disable the service; got %v", disable)
	}
}

// TestAutostartMarkerInstallUninstall proves Fix-1's persisted marker lifecycle
// under a temp home, with no live podman/launchctl (the fakeRunner records every
// call and never executes). install writes the marker; uninstall removes it.
func TestAutostartMarkerInstallUninstall(t *testing.T) {
	home := t.TempDir()
	marker := autostartMarkerPath(home)

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker should not exist before install; stat err=%v", err)
	}

	f := newFakeRunner()
	cfg := defaultPodmanConfig()
	if err := podmanAutostartInstall(context.Background(), f, io.Discard, cfg, home, "/opt/homebrew/bin/podman"); err != nil {
		t.Fatalf("install error: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("install must write the marker %s; stat err=%v", marker, err)
	}

	if err := podmanAutostartUninstall(context.Background(), f, io.Discard, cfg, home); err != nil {
		t.Fatalf("uninstall error: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("uninstall must remove the marker; stat err=%v", err)
	}

	// Uninstall again must be a no-op (marker already gone, os.IsNotExist ignored).
	if err := podmanAutostartUninstall(context.Background(), f, io.Discard, cfg, home); err != nil {
		t.Fatalf("second uninstall must not error on a missing marker: %v", err)
	}
}

// TestAutostartConfiguredDetection proves `up`'s detection helper: true when the
// marker exists, true (fallback) when only the LaunchAgent plist exists, false
// when neither is present.
func TestAutostartConfiguredDetection(t *testing.T) {
	// Neither present.
	empty := t.TempDir()
	if autostartConfigured(empty) {
		t.Error("autostartConfigured must be false when nothing is installed")
	}

	// Marker only.
	markerHome := t.TempDir()
	marker := autostartMarkerPath(markerHome)
	if err := os.MkdirAll(dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !autostartConfigured(markerHome) {
		t.Error("autostartConfigured must be true when the marker exists")
	}

	// LaunchAgent plist only (fallback path).
	plistHome := t.TempDir()
	plist := launchAgentPath(plistHome)
	if err := os.MkdirAll(dir(plist), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plist, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !autostartConfigured(plistHome) {
		t.Error("autostartConfigured must fall back to the LaunchAgent plist")
	}
}

// dir is a tiny filepath.Dir shim kept local to the test to avoid an import.
func dir(p string) string {
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "."
	}
	return p[:i]
}

// TestMachineStartArgv pins the command the LaunchAgent runs.
func TestMachineStartArgv(t *testing.T) {
	cfg := defaultPodmanConfig()
	want := []string{"machine", "start", "podman-machine-default"}
	if !slices.Equal(cfg.machineStartArgv(), want) {
		t.Errorf("machineStartArgv = %v, want %v", cfg.machineStartArgv(), want)
	}
}
