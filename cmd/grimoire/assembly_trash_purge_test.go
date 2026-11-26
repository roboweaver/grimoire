package main

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"
)

// M22 task 12.1: assembly tests for the trash purge, through the real
// buildHandler and buildScheduler (Req 20.2, 22, 23). They fail until task 12.2
// passes WithTrashPurge and WithDeleteCapabilitySuffixes in buildScheduler and
// replaces the constant trashDays of buildHandler with the configured value.
//
// Rows are trashed through REST and then backdated with SQL: the purge compares
// _wp_trash_meta_time with the real clock, so no clock seam is needed. The
// scheduler ticks every second (customWriteDeps), so a purge happens within a
// few seconds of Run starting.

const purgeWait = 6 * time.Second

// backdateTrash sets the _wp_trash_meta_time of id to days days ago.
func backdateTrash(t *testing.T, env customWriteEnv, id int64, days int) {
	t.Helper()
	at := time.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix()
	trashExec(t, env, "UPDATE "+env.Prefix+"postmeta SET meta_value = ? WHERE post_id = ? AND meta_key = '_wp_trash_meta_time'",
		strconv.FormatInt(at, 10), id)
	if got := trashMeta(t, env, id, "_wp_trash_meta_time"); got != strconv.FormatInt(at, 10) {
		t.Fatalf("backdate of %d: _wp_trash_meta_time = %q, want %d", id, got, at)
	}
}

// trashedRow creates a published row through REST, trashes it through REST and
// backdates its trash time by days days.
func trashedRow(t *testing.T, env customWriteEnv, c trashCase, title string, days int) int64 {
	t.Helper()
	id := createRESTRow(t, env, c.actor(env), c.base, title)
	trashWant(t, trashDelete(t, env, c.actor(env), c.base, id, false), http.StatusOK, "trash "+title)
	backdateTrash(t, env, id, days)
	return id
}

// startPurgeScheduler runs the scheduler built by buildScheduler until the test ends.
func startPurgeScheduler(t *testing.T, deps serverDeps) {
	t.Helper()
	_, postWrite, err := buildHandler(deps)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go buildScheduler(deps, postWrite).Run(ctx)
}

// waitGone waits until the row is gone and reports whether it was.
func waitGone(t *testing.T, env customWriteEnv, id int64) bool {
	t.Helper()
	deadline := time.Now().Add(purgeWait)
	for time.Now().Before(deadline) {
		if !rowExists(t, env, id) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// TestBuildSchedulerTrashPurge (Req 22.2, 23.2): an expired trashed post, page
// and registered-type row are purged with the M18-M20 cleanup; a fresh trashed
// row, an expired trashed row without _wp_trash_meta_time and an expired
// trashed row of an unmanaged type stay.
func TestBuildSchedulerTrashPurge(t *testing.T) {
	for _, c := range trashCases() {
		t.Run(c.name, func(t *testing.T) {
			env := newCustomWriteHandler(t, true)
			// Rows that must stay are created first: the purge walks by id, so
			// they are examined before the subject disappears.
			fresh := trashedRow(t, env, c, "Fresh", 1)
			noMeta := trashedRow(t, env, c, "Nometa", 90)
			trashExec(t, env, "DELETE FROM "+env.Prefix+"postmeta WHERE post_id = ? AND meta_key = '_wp_trash_meta_time'", noMeta)
			order := seedRawPost(t, env, "shop_order", "trash", 0)
			seedRawMeta(t, env, order, "_wp_trash_meta_time", strconv.FormatInt(time.Now().Add(-90*24*time.Hour).Unix(), 10))
			orderBefore := rowSnapshot(t, env, order)

			f := seedTrashFixture(t, env, c)
			trashWant(t, trashDelete(t, env, c.actor(env), c.base, f.id, false), http.StatusOK, "trash subject")
			backdateTrash(t, env, f.id, 31)

			startPurgeScheduler(t, customWriteDeps(t, env, customWriteRegistry(t)))
			if !waitGone(t, env, f.id) {
				t.Fatalf("expired trashed %s %d was not purged within %s", c.name, f.id, purgeWait)
			}
			assertPurged(t, env, f, c)
			for name, id := range map[string]int64{"fresh trashed row": fresh, "row without _wp_trash_meta_time": noMeta} {
				if !rowExists(t, env, id) {
					t.Errorf("%s %d was purged", name, id)
				}
			}
			if after := rowSnapshot(t, env, order); after != orderBefore {
				t.Errorf("unmanaged shop_order row changed or was purged\nbefore: %s\nafter:  %s", orderBefore, after)
			}
		})
	}
}

// TestBuildSchedulerTrashPurgeAllTypes: the three managed surfaces purge in one
// pass of one scheduler, so a missing capability suffix or type is caught.
func TestBuildSchedulerTrashPurgeAllTypes(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	var ids []int64
	for _, c := range trashCases() {
		ids = append(ids, trashedRow(t, env, c, "Expired"+c.typ, 45))
	}
	startPurgeScheduler(t, customWriteDeps(t, env, customWriteRegistry(t)))
	for i, id := range ids {
		if !waitGone(t, env, id) {
			t.Errorf("expired trashed %s %d was not purged within %s", trashCases()[i].name, id, purgeWait)
		}
	}
}

// TestBuildHandlerTrashDaysConfig (Req 20.2): content.trash_days is honored by
// the content service and the purge. With 7 days, a row trashed 10 days ago is
// purged and one trashed 3 days ago stays; the default 30 would keep both.
func TestBuildHandlerTrashDaysConfig(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	c := trashCases()[0]
	recent := trashedRow(t, env, c, "Recent", 3)
	old := trashedRow(t, env, c, "Older", 10)
	deps := customWriteDeps(t, env, customWriteRegistry(t))
	days := 7
	deps.Config.Content.TrashDays = &days
	startPurgeScheduler(t, deps)
	if !waitGone(t, env, old) {
		t.Fatalf("row trashed 10 days ago was not purged with trash_days: 7")
	}
	if !rowExists(t, env, recent) {
		t.Errorf("row trashed 3 days ago was purged with trash_days: 7")
	}
}

// TestBuildHandlerTrashDaysZero (Req 20.2, 22.2): with trash_days 0 a non-force
// DELETE answers 501 on REST and 409 on the admin API and changes nothing, force
// still deletes, and the purge is off.
func TestBuildHandlerTrashDaysZero(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	// An expired trashed row prepared before trash is switched off.
	expired := trashedRow(t, env, trashCases()[0], "Expired", 90)

	deps := customWriteDeps(t, env, customWriteRegistry(t))
	zero := 0
	deps.Config.Content.TrashDays = &zero
	h, _, err := buildHandler(deps)
	if err != nil {
		t.Fatalf("buildHandler: %v", err)
	}
	env.Handler = h

	for _, c := range trashCases() {
		t.Run(c.name, func(t *testing.T) {
			id := createRESTRow(t, env, c.actor(env), c.base, "Live"+c.typ)
			before := rowSnapshot(t, env, id)
			rec := trashDelete(t, env, c.actor(env), c.base, id, false)
			trashWant(t, rec, http.StatusNotImplemented, "REST DELETE with trash_days 0")
			if code := trashDecode(t, rec)["code"]; code != "rest_trash_not_supported" {
				t.Errorf("code = %v, want rest_trash_not_supported", code)
			}
			rec = adminDelete(t, env, c.actor(env), id, false)
			trashWant(t, rec, http.StatusConflict, "admin DELETE with trash_days 0")
			if code := adminErrorCode(t, rec); code != "trash_unsupported" {
				t.Errorf("admin code = %v, want trash_unsupported", code)
			}
			if after := rowSnapshot(t, env, id); after != before {
				t.Errorf("a refused trash changed the row\nbefore: %s\nafter:  %s", before, after)
			}
			trashWant(t, trashDelete(t, env, c.actor(env), c.base, id, true), http.StatusOK, "REST DELETE force")
			if rowExists(t, env, id) {
				t.Errorf("force delete left the row")
			}
		})
	}

	startPurgeScheduler(t, deps)
	time.Sleep(2500 * time.Millisecond) // two ticks
	if !rowExists(t, env, expired) {
		t.Errorf("expired trashed row %d was purged with trash_days: 0", expired)
	}
}
