package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// fakeRunner records every Run/Capture call and lets a test script the output
// and error of each. It never executes anything.
type fakeRunner struct {
	runCalls     [][]string // argv of each Run (name prepended)
	captureCalls [][]string // argv of each Capture (name prepended)

	// existsOK, when set, reports whether a given "<thing> exists" probe should
	// return success (nil error). Keyed by the resource name (last argv arg).
	existsOK map[string]bool

	// healthSequence is returned by successive health Captures. The last value
	// repeats once exhausted.
	healthSequence []string
	healthIdx      int
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{existsOK: map[string]bool{}}
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) error {
	f.runCalls = append(f.runCalls, append([]string{name}, args...))
	return nil
}

func (f *fakeRunner) Capture(_ context.Context, name string, args ...string) (string, error) {
	f.captureCalls = append(f.captureCalls, append([]string{name}, args...))
	full := append([]string{name}, args...)

	// Health inspect: return the scripted sequence.
	if len(args) >= 1 && args[0] == "inspect" {
		if len(f.healthSequence) == 0 {
			return "healthy", nil
		}
		v := f.healthSequence[f.healthIdx]
		if f.healthIdx < len(f.healthSequence)-1 {
			f.healthIdx++
		}
		return v, nil
	}

	// "... exists": success/failure from existsOK keyed by the last argv arg.
	if contains(full, "exists") {
		name := full[len(full)-1]
		if f.existsOK[name] {
			return "", nil
		}
		return "", errors.New("not found")
	}

	// machine info and everything else: succeed with empty output.
	return "", nil
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// fastUpOptions makes the health poll near-instant for tests.
func fastUpOptions() upOptions {
	return upOptions{pollEvery: time.Millisecond, healthLimit: time.Second}
}

// TestWaitForHealthyLoopsUntilHealthy proves the gate waits through non-healthy
// statuses and then succeeds.
func TestWaitForHealthyLoopsUntilHealthy(t *testing.T) {
	f := newFakeRunner()
	f.healthSequence = []string{"starting", "starting", "healthy"}
	err := waitForHealthy(context.Background(), f, defaultPodmanConfig(), fastUpOptions(), io.Discard)
	if err != nil {
		t.Fatalf("waitForHealthy returned error: %v", err)
	}
}

// TestWaitForHealthyTimesOut proves it returns a clear, log-pointing error when
// the DB never becomes healthy.
func TestWaitForHealthyTimesOut(t *testing.T) {
	f := newFakeRunner()
	f.healthSequence = []string{"starting"} // never healthy
	opts := upOptions{pollEvery: time.Millisecond, healthLimit: 5 * time.Millisecond}
	err := waitForHealthy(context.Background(), f, defaultPodmanConfig(), opts, io.Discard)
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "podman logs") {
		t.Errorf("timeout error must point at `podman logs`; got %v", err)
	}
}

// TestUpDependencyOrder proves the recorded Run order is: DB container run ->
// migrate -check -> migrate -overlay -> WordPress run -> grimoire run. The
// health poll sits between the DB run and the migration (asserted implicitly by
// podmanUp returning only after waitForHealthy).
func TestUpDependencyOrder(t *testing.T) {
	f := newFakeRunner()
	// Secrets must exist or up aborts before starting anything.
	f.existsOK["wp-db-password"] = true
	f.existsOK["wp-db-root-password"] = true
	f.healthSequence = []string{"healthy"}

	cfg := defaultPodmanConfig()
	if err := podmanUp(context.Background(), f, io.Discard, cfg, fastUpOptions()); err != nil {
		t.Fatalf("podmanUp error: %v", err)
	}

	order := []struct {
		label string
		match func([]string) bool
	}{
		{"db container run", func(a []string) bool { return isRunOf(a, cfg.DBCtr) }},
		{"migrate -check", func(a []string) bool { return contains(a, "-check") }},
		{"migrate -overlay", func(a []string) bool { return contains(a, "-overlay") }},
		{"wordpress container run", func(a []string) bool { return isRunOf(a, cfg.WebCtr) }},
		{"grimoire container run", func(a []string) bool { return isRunOf(a, cfg.GrimoireCtr) }},
	}

	idx := 0
	for _, call := range f.runCalls {
		if idx < len(order) && order[idx].match(call) {
			idx++
		}
	}
	if idx != len(order) {
		t.Fatalf("dependency order not satisfied; reached step %d (%s). runCalls=%v",
			idx, order[min(idx, len(order)-1)].label, f.runCalls)
	}
}

// TestUpRestartPolicyFlowsToRunArgv proves Fix-1 end to end: driving podmanUp
// with a cfg whose RestartPolicy="always" records a `run` for each of
// wp-mysql/wp-wordpress/grimoire that includes --restart=always, while an empty
// policy records none.
func TestUpRestartPolicyFlowsToRunArgv(t *testing.T) {
	run := func(policy string) [][]string {
		f := newFakeRunner()
		f.existsOK["wp-db-password"] = true
		f.existsOK["wp-db-root-password"] = true
		f.healthSequence = []string{"healthy"}
		cfg := defaultPodmanConfig()
		cfg.RestartPolicy = policy
		if err := podmanUp(context.Background(), f, io.Discard, cfg, fastUpOptions()); err != nil {
			t.Fatalf("podmanUp(policy=%q) error: %v", policy, err)
		}
		return f.runCalls
	}

	cfg := defaultPodmanConfig()
	// With "always": each stack container's run carries --restart=always.
	withAlways := run("always")
	for _, ctr := range []string{cfg.DBCtr, cfg.WebCtr, cfg.GrimoireCtr} {
		found := false
		for _, call := range withAlways {
			if isRunOf(call, ctr) && contains(call, "--restart=always") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("run of %s must carry --restart=always; runCalls=%v", ctr, withAlways)
		}
	}

	// With "": no run (stack or one-shot) carries --restart in any form.
	withNone := run("")
	for _, call := range withNone {
		for _, a := range call {
			if strings.HasPrefix(a, "--restart") {
				t.Errorf("no run should carry --restart with empty policy; got %v", call)
			}
		}
	}
}

// isRunOf reports whether argv is a `podman run ... --name <ctr> ...`.
func isRunOf(argv []string, ctr string) bool {
	if len(argv) < 2 || argv[0] != "podman" || argv[1] != "run" {
		return false
	}
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == "--name" && argv[i+1] == ctr {
			return true
		}
	}
	return false
}

// TestUpFailsFastOnMissingSecret proves up stops with a fix-pointing error when
// a required secret is absent, before starting any container.
func TestUpFailsFastOnMissingSecret(t *testing.T) {
	f := newFakeRunner() // existsOK empty => secrets missing
	err := podmanUp(context.Background(), f, io.Discard, defaultPodmanConfig(), fastUpOptions())
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
	if !strings.Contains(err.Error(), "secret") {
		t.Errorf("error should mention the missing secret; got %v", err)
	}
	for _, call := range f.runCalls {
		if isRunOf(call, "wp-mysql") {
			t.Error("no container should have been started when a secret is missing")
		}
	}
}

// TestUpIdempotentStartsExistingContainer proves that when a container already
// exists, up starts its pod rather than running a fresh container.
func TestUpIdempotentStartsExistingContainer(t *testing.T) {
	f := newFakeRunner()
	f.existsOK["wp-db-password"] = true
	f.existsOK["wp-db-root-password"] = true
	// Everything already exists.
	f.existsOK["wp-net"] = true
	for _, v := range []string{"wp-db-data", "wp-html", "wp-content"} {
		f.existsOK[v] = true
	}
	for _, p := range []string{"wp-db-pod", "wp-web-pod", "grimoire-pod"} {
		f.existsOK[p] = true
	}
	for _, c := range []string{"wp-mysql", "wp-wordpress", "grimoire"} {
		f.existsOK[c] = true
	}
	f.healthSequence = []string{"healthy"}

	cfg := defaultPodmanConfig()
	if err := podmanUp(context.Background(), f, io.Discard, cfg, fastUpOptions()); err != nil {
		t.Fatalf("podmanUp error: %v", err)
	}

	// The migrate one-shots (`podman run --rm ... grimoire-cli`) still run; what
	// must NOT happen is a fresh `run` of any of the three long-lived stack
	// containers, since they already exist.
	for _, ctr := range []string{cfg.DBCtr, cfg.WebCtr, cfg.GrimoireCtr} {
		for _, call := range f.runCalls {
			if isRunOf(call, ctr) {
				t.Errorf("container %s exists; it must be started, not re-run; got %v", ctr, call)
			}
		}
	}
	started := 0
	for _, call := range f.runCalls {
		if len(call) >= 2 && call[1] == "pod" && contains(call, "start") {
			started++
		}
	}
	if started != 3 {
		t.Errorf("expected 3 pod starts for existing containers; got %d (%v)", started, f.runCalls)
	}
}
