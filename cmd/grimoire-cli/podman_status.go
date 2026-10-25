package main

// podman_status.go — "status": show pod/container state and probe the key
// endpoints.

import (
	"context"
	"fmt"
	"io"
)

// podmanStatus prints the pod and container state, then probes the key
// endpoints: grimoire's /healthz and REST post list, and the WordPress home
// page. Endpoint probing goes through the injectable prober so this is testable
// without a running server; the state queries go through Runner.Capture so they
// are testable without a running podman.
func podmanStatus(ctx context.Context, r Runner, probe httpProber, w io.Writer, cfg podmanConfig) error {
	fmt.Fprintln(w, "=== Pods ===")
	if out, err := r.Capture(ctx, "podman", "pod", "ps"); err == nil {
		fmt.Fprintln(w, out)
	} else {
		fmt.Fprintf(w, "  could not list pods: %v\n", err)
	}

	fmt.Fprintln(w, "=== Containers ===")
	if out, err := r.Capture(ctx, "podman", "ps", "-a", "--format",
		"table {{.Names}}\t{{.Status}}\t{{.Ports}}"); err == nil {
		fmt.Fprintln(w, out)
	} else {
		fmt.Fprintf(w, "  could not list containers: %v\n", err)
	}

	fmt.Fprintln(w, "=== Endpoints ===")
	for _, p := range cfg.statusProbes() {
		fmt.Fprintf(w, "  %-34s -> %s\n", p.Label, probe(ctx, p.URL))
	}
	return nil
}
