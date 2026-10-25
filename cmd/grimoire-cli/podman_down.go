package main

// podman_down.go — "down": stop and remove the pods while preserving every bit
// of data.

import (
	"context"
	"fmt"
	"io"
)

// podmanDown removes the three pods but NOT the volumes, secrets or network, so
// the database, the wp-content tree and the generated passwords all survive. A
// later "up" brings the stack back with the same data.
//
// Pods are removed grimoire-first, then WordPress, then the database, i.e. the
// reverse of the start order, so dependents stop before the thing they depend
// on. Each removal tolerates an already-gone pod (podman rm -f) so "down" is
// safe to run twice.
func podmanDown(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig) error {
	fmt.Fprintln(w, "Removing pods (volumes, secrets and network are preserved — data survives)...")
	for _, pod := range []string{cfg.GrimoirePod, cfg.WebPod, cfg.DBPod} {
		fmt.Fprintf(w, "  removing %s\n", pod)
		if err := r.Run(ctx, "podman", cfg.podRmArgv(pod)...); err != nil {
			// A missing pod is fine; keep going so a half-torn-down stack still
			// finishes cleanly.
			fmt.Fprintf(w, "  (%s was already gone or could not be removed: %v)\n", pod, err)
		}
	}
	fmt.Fprintf(w, "Done. The database volume %q, the wp-content volume %q and the "+
		"secrets are untouched. Run `grimoire-cli podman up` to bring the stack back "+
		"with the same data.\n", cfg.DBVolume, cfg.ContentVolume)
	return nil
}
