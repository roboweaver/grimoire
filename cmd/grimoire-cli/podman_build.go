package main

// podman_build.go — "build": build the grimoire image from the repo's
// Containerfile.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// podmanBuild builds the grimoire image (localhost/grimoire:dev by default) from
// the repo's Containerfile. The build context is resolved by walking up from
// contextHint (or the current directory) to the first directory containing a
// Containerfile; an explicit context that already holds one is used as-is. A
// friendly error points at the fix when none is found.
func podmanBuild(ctx context.Context, r Runner, w io.Writer, cfg podmanConfig, contextHint string) error {
	root, err := findContainerfile(contextHint)
	if err != nil {
		return err
	}
	containerfile := filepath.Join(root, "Containerfile")
	fmt.Fprintf(w, "Building %s from %s...\n", cfg.GrimoireImage, containerfile)
	if err := r.Run(ctx, "podman", cfg.buildArgv(containerfile, root)...); err != nil {
		return fmt.Errorf("podman build failed: %w", err)
	}
	fmt.Fprintf(w, "Built %s.\n", cfg.GrimoireImage)
	return nil
}

// findContainerfile returns the directory holding the Containerfile, searching
// from start (defaulting to the current working directory) upward through
// parents. This lets `build` work from anywhere inside the repo.
func findContainerfile(start string) (string, error) {
	dir := start
	if dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(abs, "Containerfile")); err == nil {
			return abs, nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no Containerfile found at or above %q; run `build` "+
				"from inside the grimoire repo, or pass -context <repo-root>", dir)
		}
		abs = parent
	}
}
