package main

// podman_autostart.go — "autostart": configure (or remove) auto-start of the
// stack after a reboot or a podman restart, on macOS. Two layers:
//
//	Layer A — containers return when the podman VM restarts. Set --restart always
//	          on the three stack containers and enable the in-VM
//	          podman-restart.service so systemd brings them back.
//	Layer B — the podman machine VM itself starts at Mac login. A macOS
//	          LaunchAgent plist runs `podman machine start` with RunAtLoad.
//
// Caveat worth stating plainly: a LaunchAgent fires at USER LOGIN, not at the
// pre-login boot screen, so the stack returns after you log in, not before.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// launchAgentLabel is the reverse-DNS label for the LaunchAgent. Kept stable so
// install/uninstall and `launchctl` target the same agent and a second one is
// never spawned alongside it.
const launchAgentLabel = "dev.grimoire.podman-machine"

// launchAgentLog is where the LaunchAgent writes its stdout/stderr.
const launchAgentLog = "/tmp/podman-machine-autostart.log"

// plistData is the template input for the LaunchAgent.
type plistData struct {
	Label       string
	PodmanPath  string
	MachineName string
	LogPath     string
}

// launchAgentTemplate renders a standard macOS LaunchAgent that runs
// `podman machine start <machine>` once at login (RunAtLoad), logging to a
// fixed file. KeepAlive is intentionally omitted: `podman machine start` exits
// after the VM is up, and KeepAlive would fight that by relaunching it.
var launchAgentTemplate = template.Must(template.New("plist").Parse(
	`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.PodmanPath}}</string>
		<string>machine</string>
		<string>start</string>
		<string>{{.MachineName}}</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogPath}}</string>
	<key>StandardErrorPath</key>
	<string>{{.LogPath}}</string>
</dict>
</plist>
`))

// plistContent renders the LaunchAgent plist XML for cfg. Pure (no filesystem,
// no launchctl) so the exact content is unit-testable. podmanPath is the
// absolute path to the podman binary, which a LaunchAgent needs because it runs
// with a minimal PATH.
func plistContent(cfg podmanConfig, podmanPath string) (string, error) {
	var buf bytes.Buffer
	err := launchAgentTemplate.Execute(&buf, plistData{
		Label:       launchAgentLabel,
		PodmanPath:  podmanPath,
		MachineName: cfg.Machine,
		LogPath:     launchAgentLog,
	})
	if err != nil {
		return "", err
	}
	return buf.String(), nil
}

// launchAgentPath is the on-disk location of the plist for the current user.
func launchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchAgentLabel+".plist")
}

// autostartMarkerPath is the on-disk marker `autostart install` writes to record
// that autostart is configured. `up` checks for it (falling back to the
// LaunchAgent plist) and, when present, reapplies the --restart always policy so
// the autostart intent is not silently lost when `up` recreates containers after
// a `down`.
func autostartMarkerPath(home string) string {
	return filepath.Join(home, ".local", "share", "grimoire", "podman-autostart")
}

// podmanAutostartInstall wires both layers on.
func podmanAutostartInstall(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig, home, podmanPath string) error {
	fmt.Fprintln(w, "Autostart — Layer A: containers return when the podman VM restarts.")
	fmt.Fprintf(w, "  setting --restart always on %v\n", cfg.restartContainers())
	if err := r.Run(ctx, "podman", cfg.updateRestartArgv("always")...); err != nil {
		return fmt.Errorf("set --restart always: %w (are the containers created? run `up` first)", err)
	}
	fmt.Fprintln(w, "  enabling podman-restart.service inside the VM")
	if err := r.Run(ctx, "podman", cfg.machineSSHSystemctlArgv("enable")...); err != nil {
		return fmt.Errorf("enable podman-restart.service: %w (is the podman machine running?)", err)
	}

	fmt.Fprintln(w, "Autostart — Layer B: the podman VM starts at Mac login.")
	content, err := plistContent(cfg, podmanPath)
	if err != nil {
		return err
	}
	path := launchAgentPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create LaunchAgents dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write LaunchAgent plist: %w", err)
	}
	fmt.Fprintf(w, "  wrote %s\n", path)
	if err := r.Run(ctx, "launchctl", "load", path); err != nil {
		return fmt.Errorf("launchctl load: %w", err)
	}

	// Persist a marker so `up` can detect that autostart is configured and
	// reapply --restart always when it recreates containers after a `down`.
	marker := autostartMarkerPath(home)
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		return fmt.Errorf("create autostart marker dir: %w", err)
	}
	markerContent := "podman autostart installed " + time.Now().UTC().Format(time.RFC3339) + "\n"
	if err := os.WriteFile(marker, []byte(markerContent), 0o644); err != nil {
		return fmt.Errorf("write autostart marker: %w", err)
	}
	fmt.Fprintf(w, "  wrote autostart marker %s (so `up` reapplies --restart always)\n", marker)

	fmt.Fprintln(w, "Installed. NOTE: the LaunchAgent fires at USER LOGIN, not at the "+
		"pre-login boot screen, so the stack returns after you log in.")
	return nil
}

// podmanAutostartUninstall reverses both layers.
func podmanAutostartUninstall(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig, home string) error {
	fmt.Fprintln(w, "Removing autostart — Layer B first.")
	path := launchAgentPath(home)
	if err := r.Run(ctx, "launchctl", "unload", path); err != nil {
		fmt.Fprintf(w, "  (launchctl unload reported: %v — continuing)\n", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(w, "  (could not remove %s: %v)\n", path, err)
	} else {
		fmt.Fprintf(w, "  removed %s\n", path)
	}

	marker := autostartMarkerPath(home)
	if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
		fmt.Fprintf(w, "  (could not remove autostart marker %s: %v)\n", marker, err)
	} else {
		fmt.Fprintf(w, "  removed autostart marker %s\n", marker)
	}

	fmt.Fprintln(w, "Layer A: clearing --restart and disabling podman-restart.service.")
	if err := r.Run(ctx, "podman", cfg.updateRestartArgv("no")...); err != nil {
		fmt.Fprintf(w, "  (could not clear --restart: %v)\n", err)
	}
	if err := r.Run(ctx, "podman", cfg.machineSSHSystemctlArgv("disable")...); err != nil {
		fmt.Fprintf(w, "  (could not disable podman-restart.service: %v)\n", err)
	}
	fmt.Fprintln(w, "Autostart removed.")
	return nil
}
