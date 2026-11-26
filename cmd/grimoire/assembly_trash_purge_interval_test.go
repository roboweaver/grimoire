package main

import (
	"testing"
	"time"
)

// M23 task 2.3: the configured scheduler.trash_purge_interval_seconds reaches
// the scheduler through the real buildScheduler (Req 9.1).
//
// The scheduler ticks every second (customWriteDeps). With a 1s purge interval
// the first scan purges an expired row and a later scan purges a second row that
// expired afterwards. With the default hourly cadence the second row would stay,
// so observing it purged proves the configured value was used.

// TestBuildSchedulerTrashPurgeInterval (Req 9.1): a second expired row, backdated
// after the first purge scan, is purged on a later tick when the interval is 1s.
func TestBuildSchedulerTrashPurgeInterval(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	c := trashCases()[0]
	first := trashedRow(t, env, c, "First", 45)
	deps := customWriteDeps(t, env, customWriteRegistry(t))
	secs := 1
	deps.Config.Scheduler.TrashPurgeIntervalSeconds = &secs
	startPurgeScheduler(t, deps)
	if !waitGone(t, env, first) {
		t.Fatalf("first expired row %d was not purged within %s", first, purgeWait)
	}
	// Created and backdated only after the first scan has run.
	second := trashedRow(t, env, c, "Second", 45)
	if !waitGone(t, env, second) {
		t.Fatalf("second expired row %d was not purged within %s with trash_purge_interval_seconds: 1; the configured interval did not reach the scheduler", second, purgeWait)
	}
}

// TestBuildSchedulerTrashPurgeIntervalDefault: with the key absent the cadence
// stays hourly, so a row expired after the first scan is not purged within a few ticks.
func TestBuildSchedulerTrashPurgeIntervalDefault(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	c := trashCases()[0]
	first := trashedRow(t, env, c, "First", 45)
	startPurgeScheduler(t, customWriteDeps(t, env, customWriteRegistry(t)))
	if !waitGone(t, env, first) {
		t.Fatalf("first expired row %d was not purged within %s", first, purgeWait)
	}
	second := trashedRow(t, env, c, "Second", 45)
	time.Sleep(3 * time.Second)
	if !rowExists(t, env, second) {
		t.Errorf("second expired row %d was purged within the default hourly interval", second)
	}
}
