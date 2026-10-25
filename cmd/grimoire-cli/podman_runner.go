package main

// podman_runner.go — the injectable command runner and HTTP prober.
//
// Every function that builds and executes podman argv takes a Runner, and the
// status probe takes an httpProber, so tests can assert the exact argv and the
// exact endpoints WITHOUT executing podman or hitting the network. Production
// code wires execRunner (os/exec) and httpGet (net/http); tests wire fakes.
//
// Pure Go, no cgo, no podman SDK — only os/exec and net/http from the stdlib.

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Runner abstracts running an external command. Run streams the child's
// stdout/stderr through to the process's own, for the long, chatty commands
// (up/build/down). Capture returns the trimmed combined output, for the short
// query commands (podman inspect health polling, status queries) whose output
// the caller needs to inspect rather than show.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
	Capture(ctx context.Context, name string, args ...string) (string, error)
}

// execRunner is the production Runner over os/exec.CommandContext.
type execRunner struct{}

// Run executes name with args, wiring the child's stdout/stderr/stdin to the
// parent process so the user sees podman's output live.
func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// Capture executes name with args and returns the trimmed combined output. The
// callers (health polling, resource-exists checks, status queries) want the
// text, not a live stream, and treat a non-zero exit as a normal "no" rather
// than a fatal error, so they inspect the returned error themselves.
func (execRunner) Capture(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// httpProber probes an endpoint and returns a short status token (an HTTP
// status code like "200", or "down" when the request fails). It is injected
// into status so endpoint probing is testable without a live server.
type httpProber func(ctx context.Context, url string) string

// httpGet is the production prober: a GET with a short timeout, reporting the
// numeric status code on success and "down" on any transport error.
func httpGet(ctx context.Context, url string) string {
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return "down"
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "down"
	}
	defer resp.Body.Close()
	return strconv.Itoa(resp.StatusCode)
}

// atoiOK parses a base-10 int, reporting whether it was a clean parse. Used by
// applyEnv so a malformed env value is ignored rather than silently zeroing a
// port.
func atoiOK(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// hostURL formats http://localhost:<port><path> for the status probes.
func hostURL(port int, path string) string {
	return fmt.Sprintf("http://localhost:%d%s", port, path)
}
