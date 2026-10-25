package main

// podman.go — the "podman" subcommand dispatcher.
//
// This mirrors the plain flag-package style of the rest of grimoire-cli: a
// switch on the first argument, one flag.NewFlagSet per subcommand, and rich
// Usage text that carries the developer guidance the external runbook used to
// hold. There is no cobra/urfave-cli dependency and none is added.
//
// The command manages the local Podman dev stack: WordPress and grimoire
// sharing one MySQL database. It shells out to the `podman` binary via the
// injected Runner; it never links a podman SDK.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// parseFlags parses args and treats a -h/--help request as a clean, non-error
// outcome: flag.FlagSet already printed the usage, so the caller should return
// nil and exit 0 rather than surface "flag: help requested" as an error. The
// bool reports "stop here, with no error".
func parseFlags(fs *flag.FlagSet, args []string) (done bool, err error) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return true, err
	}
	return false, nil
}

// runPodman dispatches "podman <up|down|status|build|autostart ...>".
func runPodman(args []string) error {
	if len(args) < 1 {
		podmanUsage(os.Stderr)
		return errors.New("podman: a subcommand is required")
	}
	ctx := context.Background()
	r := execRunner{}
	w := os.Stdout
	sub, rest := args[0], args[1:]

	switch sub {
	case "up":
		return runPodmanUp(ctx, r, w, rest)
	case "down":
		return runPodmanDown(ctx, r, w, rest)
	case "status":
		return runPodmanStatus(ctx, r, w, rest)
	case "build":
		return runPodmanBuild(ctx, r, w, rest)
	case "autostart":
		return runPodmanAutostart(ctx, r, w, rest)
	case "-h", "--help", "help":
		podmanUsage(w)
		return nil
	default:
		podmanUsage(os.Stderr)
		return fmt.Errorf("podman: unknown subcommand %q", sub)
	}
}

// podmanUsage prints the group-level help, including the shared-fate note so it
// is seen before any subcommand runs.
func podmanUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: grimoire-cli podman <up|down|status|build|autostart> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Manage the local Podman dev stack: WordPress + grimoire sharing one MySQL DB.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  up         create/start the full stack in dependency order (DB healthy first,")
	fmt.Fprintln(w, "             then overlay migration, then WordPress and grimoire). Idempotent.")
	fmt.Fprintln(w, "  down       stop/remove the pods, preserving volumes, secrets and network (data survives).")
	fmt.Fprintln(w, "  status     show pod/container state and probe grimoire and WordPress endpoints.")
	fmt.Fprintln(w, "  build      build the grimoire image from the repo's Containerfile.")
	fmt.Fprintln(w, "  autostart  install|uninstall macOS auto-start after reboot/podman restart.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "SHARED FATE: WordPress and grimoire share the same tables and the same")
	fmt.Fprintln(w, "wp-content volume with NO isolation. A write in one is visible in the other;")
	fmt.Fprintln(w, "recovery from a bad write is a restore from backups.")
}

// newPodmanFlagSet builds a flag set seeded with defaults then env overrides, so
// parsing yields the precedence flag > env > default. It attaches the rich
// per-subcommand usage text in usageText.
func newPodmanFlagSet(name, usageText string) (*flag.FlagSet, *podmanConfig) {
	cfg := defaultPodmanConfig()
	cfg.applyEnv(os.Getenv)
	fs := flag.NewFlagSet("podman "+name, flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), usageText)
		fmt.Fprintln(fs.Output(), "\nFlags:")
		fs.PrintDefaults()
	}
	cfg.addFlags(fs)
	return fs, &cfg
}

func runPodmanUp(ctx context.Context, r Runner, w io.Writer, args []string) error {
	fs, cfg := newPodmanFlagSet("up",
		"usage: grimoire-cli podman up [flags]\n\n"+
			"Create network/volumes/secrets/pods and start the whole stack in dependency\n"+
			"order: the MySQL pod first, then WAIT until wp-mysql reports healthy, then run\n"+
			"the read-only `migrate -check` preflight and the additive `migrate -overlay`\n"+
			"migration, then start WordPress and grimoire. Idempotent: existing pods and\n"+
			"containers are started rather than recreated, so data is preserved.\n\n"+
			"Gotchas it prints as it runs: the shared-fate warning; the no-underscore table\n"+
			"prefix; secrets-not-env-vars; overlay-not-greenfield migration; DB-health-before-\n"+
			"dependents ordering; and cross-pod DNS (WordPress reaches the DB as wp-db-pod:3306,\n"+
			"not localhost). The wp-db-password / wp-db-root-password secrets must already\n"+
			"exist; `up` will tell you how to create one if it is missing.")
	if done, err := parseFlags(fs, args); done {
		return err
	}
	if err := requirePodman(ctx, r); err != nil {
		return err
	}
	return podmanUp(ctx, r, w, *cfg, defaultUpOptions())
}

func runPodmanDown(ctx context.Context, r Runner, w io.Writer, args []string) error {
	fs, cfg := newPodmanFlagSet("down",
		"usage: grimoire-cli podman down [flags]\n\n"+
			"Stop and remove the three pods while PRESERVING the volumes, secrets and\n"+
			"network, so the database, the wp-content tree and the generated passwords all\n"+
			"survive. A later `up` brings the stack back with the same data. This does NOT\n"+
			"delete data; there is no destroy here on purpose.")
	if done, err := parseFlags(fs, args); done {
		return err
	}
	if err := requirePodman(ctx, r); err != nil {
		return err
	}
	return podmanDown(ctx, r, w, *cfg)
}

func runPodmanStatus(ctx context.Context, r Runner, w io.Writer, args []string) error {
	fs, cfg := newPodmanFlagSet("status",
		"usage: grimoire-cli podman status [flags]\n\n"+
			"Show pod and container state, then probe the key endpoints: grimoire's\n"+
			"/healthz and /wp-json/wp/v2/posts, and the WordPress home page. Each line\n"+
			"prints the HTTP status code, or `down` when the endpoint does not answer.")
	if done, err := parseFlags(fs, args); done {
		return err
	}
	if err := requirePodman(ctx, r); err != nil {
		return err
	}
	return podmanStatus(ctx, r, httpGet, w, *cfg)
}

func runPodmanBuild(ctx context.Context, r Runner, w io.Writer, args []string) error {
	fs, cfg := newPodmanFlagSet("build",
		"usage: grimoire-cli podman build [-context dir] [flags]\n\n"+
			"Build the grimoire image (localhost/grimoire:dev by default) from the repo's\n"+
			"Containerfile. The build context defaults to the first directory at or above\n"+
			"the current one that contains a Containerfile; override it with -context.")
	context := fs.String("context", "", "build context directory (defaults to the repo root found by walking up)")
	if done, err := parseFlags(fs, args); done {
		return err
	}
	if err := requirePodman(ctx, r); err != nil {
		return err
	}
	return podmanBuild(ctx, r, w, *cfg, *context)
}

func runPodmanAutostart(ctx context.Context, r Runner, w io.Writer, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: grimoire-cli podman autostart <install|uninstall> [flags]")
	}
	action, rest := args[0], args[1:]
	fs, cfg := newPodmanFlagSet("autostart "+action,
		"usage: grimoire-cli podman autostart <install|uninstall> [flags]\n\n"+
			"Configure macOS auto-start after a reboot or a podman restart, in two layers:\n"+
			"  Layer A — set --restart always on the three stack containers and enable the\n"+
			"            in-VM podman-restart.service, so containers return when the VM does.\n"+
			"  Layer B — write a LaunchAgent that runs `podman machine start` at login.\n\n"+
			"CAVEAT: a LaunchAgent fires at USER LOGIN, not the pre-login boot screen, so the\n"+
			"stack comes back after you log in, not before. `uninstall` reverses both layers.")
	if done, err := parseFlags(fs, rest); done {
		return err
	}
	switch action {
	case "install":
		if err := requirePodman(ctx, r); err != nil {
			return err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		podmanPath, err := exec.LookPath("podman")
		if err != nil {
			// A LaunchAgent needs an absolute path; fall back to the bare name
			// rather than fail, but tell the user it may not resolve at login.
			fmt.Fprintln(w, "warning: could not resolve an absolute podman path; the "+
				"LaunchAgent will use \"podman\" and may not find it with the login PATH.")
			podmanPath = "podman"
		}
		return podmanAutostartInstall(ctx, r, w, *cfg, home, podmanPath)
	case "uninstall":
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		return podmanAutostartUninstall(ctx, r, w, *cfg, home)
	default:
		return fmt.Errorf("podman autostart: unknown action %q (want install|uninstall)", action)
	}
}

// requirePodman gives a friendly, fix-pointing error when the prerequisite is
// missing, in the same spirit as the runbook's troubleshooting table. It checks
// that podman is on PATH and that the machine VM answers; a bare-metal Linux
// podman has no VM, so a failed `machine info` is a warning, not a hard error.
func requirePodman(ctx context.Context, r Runner) error {
	if _, err := exec.LookPath("podman"); err != nil {
		return errors.New("podman is not installed or not on PATH. Install it " +
			"(https://podman.io/getting-started/installation) and, on macOS, run " +
			"`podman machine init && podman machine start`")
	}
	if _, err := r.Capture(ctx, "podman", "machine", "info"); err != nil {
		// Not fatal: on native Linux there is no machine. But on macOS a stopped
		// VM is the most common failure, so point at the fix.
		fmt.Fprintln(os.Stderr, "note: `podman machine info` failed; if you are on "+
			"macOS the VM may be stopped — run `podman machine start`.")
	}
	return nil
}
