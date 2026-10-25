package main

// podman_up.go — the "up" orchestration: bring the whole stack up in dependency
// order, idempotently, with the database health gate and the overlay migration.

import (
	"context"
	"fmt"
	"io"
	"time"
)

// The five non-obvious invariants the runbook carried, printed to the operator
// as "up" runs so the knowledge is surfaced at the point of use and not buried
// in a doc. These strings are asserted by the tests, so the guidance cannot
// silently regress.
const (
	sharedFateWarning = "SHARED-FATE WARNING: WordPress and grimoire share the SAME database " +
		"tables and the SAME wp-content volume with NO isolation. A write in one is " +
		"immediately visible in the other, and the only way back from a bad write is a " +
		"restore from backups. Treat the pair as one dev environment."

	invariantPrefix = "Table prefix is concatenated with NO separator: the default " +
		"\"accuweaver\" means tables like \"accuweaverposts\" (no trailing underscore). " +
		"Set it wrong and both apps quietly offer a fresh-install wizard over a full database."

	invariantSecrets = "DB credentials come from podman secrets mounted at /run/secrets/*, " +
		"never plaintext env vars and never visible in `podman inspect`."

	invariantOverlay = "Schema setup is the ADDITIVE overlay migration (migrate -overlay): " +
		"grimoire-owned tables only, guarded by IF NOT EXISTS, no ALTER TABLE. The greenfield " +
		"`migrate` is never run against the populated WordPress database."

	invariantHealth = "Startup order is gated on DB health: MySQL must report healthy before " +
		"WordPress or grimoire start, so dependents never race an unready database."

	invariantDNS = "Cross-pod DNS: the pods are separate network namespaces, so WordPress " +
		"reaches the database by pod name (wp-db-pod:3306), not localhost."
)

// printGuidance writes the shared-fate warning and the five invariant
// explanations to w. Called at the top of "up".
func printGuidance(w io.Writer) {
	fmt.Fprintln(w, "========================================================================")
	fmt.Fprintln(w, sharedFateWarning)
	fmt.Fprintln(w, "------------------------------------------------------------------------")
	fmt.Fprintln(w, "Invariants this stack encodes:")
	for _, inv := range []string{invariantPrefix, invariantSecrets, invariantOverlay, invariantHealth, invariantDNS} {
		fmt.Fprintf(w, "  - %s\n", inv)
	}
	fmt.Fprintln(w, "========================================================================")
}

// upOptions tunes the health poll so tests run fast. Production uses sane
// defaults; tests pass a tiny interval and a small budget.
type upOptions struct {
	pollEvery   time.Duration
	healthLimit time.Duration
}

func defaultUpOptions() upOptions {
	return upOptions{pollEvery: 5 * time.Second, healthLimit: 200 * time.Second}
}

// podmanUp brings up the full stack. Dependency order is strict:
//
//  1. network, volumes, secrets exist (idempotent — created only if missing).
//  2. DB pod + wp-mysql start.
//  3. WAIT until wp-mysql reports healthy.
//  4. migrate -check (read-only preflight), then migrate -overlay (additive).
//  5. WordPress pod + container, then grimoire pod + container start.
//
// Idempotent throughout: if a pod/container already exists it is started rather
// than recreated, mirroring the reference bash scripts.
func podmanUp(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig, opts upOptions) error {
	printGuidance(w)

	// 1. network / volumes / secrets — create only what is missing.
	if !resourceExists(ctx, r, cfg.networkExistsArgv()) {
		fmt.Fprintf(w, "Creating network %s...\n", cfg.Network)
		if err := r.Run(ctx, "podman", cfg.networkCreateArgv()...); err != nil {
			return fmt.Errorf("create network %s: %w", cfg.Network, err)
		}
	}
	for _, v := range cfg.volumes() {
		if !resourceExists(ctx, r, cfg.volumeExistsArgv(v)) {
			fmt.Fprintf(w, "Creating volume %s...\n", v)
			if err := r.Run(ctx, "podman", cfg.volumeCreateArgv(v)...); err != nil {
				return fmt.Errorf("create volume %s: %w", v, err)
			}
		}
	}
	for _, s := range []string{cfg.RootSecret, cfg.UserSecret} {
		if !resourceExists(ctx, r, cfg.secretExistsArgv(s)) {
			return fmt.Errorf("secret %q is missing; create it first, e.g.\n"+
				"  openssl rand -base64 24 | tr -d '/+=\\n' | podman secret create %s -\n"+
				"(secrets are created out of band on purpose so no password is ever "+
				"written to a flag or this process's args)", s, s)
		}
	}

	// 2. DB pod first.
	fmt.Fprintf(w, "Starting database pod %s...\n", cfg.DBPod)
	if err := ensurePodWithContainer(ctx, r, w, cfg,
		cfg.DBPod, cfg.DBCtr, cfg.dbPodCreateArgv(), cfg.dbRunArgv()); err != nil {
		return err
	}

	// 3. health gate — nothing dependent starts until MySQL is healthy.
	fmt.Fprintln(w, "Waiting for MySQL to report healthy before starting dependents...")
	if err := waitForHealthy(ctx, r, cfg, opts, w); err != nil {
		return err
	}
	fmt.Fprintln(w, "  MySQL is healthy.")

	// 4. preflight then overlay migration — never the greenfield migrate.
	fmt.Fprintln(w, "Running read-only schema preflight (migrate -check)...")
	if err := r.Run(ctx, "podman", cfg.migrateCheckArgv()...); err != nil {
		return fmt.Errorf("migrate -check failed: %w", err)
	}
	fmt.Fprintln(w, "Applying additive overlay migration (migrate -overlay)...")
	if err := r.Run(ctx, "podman", cfg.migrateOverlayArgv()...); err != nil {
		return fmt.Errorf("migrate -overlay failed: %w", err)
	}

	// 5. dependents: WordPress then grimoire.
	fmt.Fprintf(w, "Starting WordPress pod %s...\n", cfg.WebPod)
	if err := ensurePodWithContainer(ctx, r, w, cfg,
		cfg.WebPod, cfg.WebCtr, cfg.webPodCreateArgv(), cfg.webRunArgv()); err != nil {
		return err
	}
	fmt.Fprintf(w, "Starting grimoire pod %s...\n", cfg.GrimoirePod)
	if err := ensurePodWithContainer(ctx, r, w, cfg,
		cfg.GrimoirePod, cfg.GrimoireCtr, cfg.grimoirePodCreateArgv(), cfg.grimoireRunArgv()); err != nil {
		return err
	}

	fmt.Fprintf(w, "\nStack is up. grimoire: http://localhost:%d/  WordPress: http://localhost:%d/\n",
		cfg.GrimoirePort, cfg.WordPressPort)
	fmt.Fprintf(w, "Check state with: grimoire-cli podman status\n")
	return nil
}

// resourceExists runs a "podman ... exists" probe and reports the boolean. A
// non-zero exit (the resource is absent) is a normal "false", not an error.
func resourceExists(ctx context.Context, r Runner, existsArgv []string) bool {
	_, err := r.Capture(ctx, "podman", existsArgv...)
	return err == nil
}

// ensurePodWithContainer makes a pod+container present and running, idempotently:
//   - pod absent  -> create it.
//   - container absent -> run it (which starts it).
//   - container present -> start the pod (brings a stopped container back
//     without recreating it, preserving its config and data).
func ensurePodWithContainer(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig,
	pod, ctr string, podCreateArgv, runArgv []string) error {
	if !resourceExists(ctx, r, cfg.podExistsArgv(pod)) {
		if err := r.Run(ctx, "podman", podCreateArgv...); err != nil {
			return fmt.Errorf("create pod %s: %w", pod, err)
		}
	}
	if resourceExists(ctx, r, cfg.containerExistsArgv(ctr)) {
		fmt.Fprintf(w, "  %s already exists; starting it (not recreating)\n", ctr)
		if err := r.Run(ctx, "podman", cfg.podStartArgv(pod)...); err != nil {
			return fmt.Errorf("start pod %s: %w", pod, err)
		}
		return nil
	}
	if err := r.Run(ctx, "podman", runArgv...); err != nil {
		return fmt.Errorf("run container %s: %w", ctr, err)
	}
	return nil
}

// waitForHealthy polls `podman inspect <db> {{.State.Health.Status}}` until it
// reads "healthy" or the budget in opts is exhausted. On timeout it returns a
// clear error naming the log command the operator should read, matching the
// runbook's troubleshooting guidance.
func waitForHealthy(ctx context.Context, r Runner, cfg podmanConfig, opts upOptions, w io.Writer) error {
	deadline := time.Now().Add(opts.healthLimit)
	for {
		status, _ := r.Capture(ctx, "podman", cfg.healthStatusArgv()...)
		if status == "healthy" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become healthy within %s (last status %q). "+
				"Inspect it with: podman logs %s", cfg.DBCtr, opts.healthLimit, status, cfg.DBCtr)
		}
		fmt.Fprintf(w, "  %s health=%q, waiting...\n", cfg.DBCtr, status)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(opts.pollEvery):
		}
	}
}
