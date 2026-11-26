package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"testing/quick"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// Tests for the hourly trash purge (task 11.1(b); Req 22, D7, sign-off 10, 11).
// They fail to compile until WithTrashPurge, WithDeleteCapabilitySuffixes and
// withClock exist.

// prgClock is a settable clock for withClock.
type prgClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *prgClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *prgClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// prgLister is a domain.TrashedPostLister over a fixed row set. It honours the
// cursor and the limit and records every call.
type prgLister struct {
	mu    sync.Mutex
	rows  []domain.TrashedPost
	calls []prgListCall
	err   error
}

type prgListCall struct {
	types []string
	after int64
	limit int
}

func (l *prgLister) TrashedPosts(_ context.Context, types []string, after int64, limit int) ([]domain.TrashedPost, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, prgListCall{types: append([]string(nil), types...), after: after, limit: limit})
	if l.err != nil {
		return nil, l.err
	}
	var out []domain.TrashedPost
	for _, r := range l.rows {
		if r.Post.ID > after && len(out) < limit {
			out = append(out, r)
		}
	}
	return out, nil
}

func (l *prgLister) callCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.calls)
}

// prgDeleter is the trashDeleter fake: Delete records the actor and the post,
// ByID answers from status (default trash).
type prgDeleter struct {
	mu        sync.Mutex
	deleted   []int64
	actors    []auth.Principal
	failWith  map[int64]error
	status    map[int64]string
	byIDCalls []int64
}

func (d *prgDeleter) Delete(_ context.Context, actor auth.Principal, p domain.Post) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.actors = append(d.actors, actor)
	if err := d.failWith[p.ID]; err != nil {
		return err
	}
	d.deleted = append(d.deleted, p.ID)
	return nil
}

func (d *prgDeleter) ByID(_ context.Context, id int64) (domain.Post, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byIDCalls = append(d.byIDCalls, id)
	st := "trash"
	if s, ok := d.status[id]; ok {
		st = s
	}
	return domain.Post{ID: id, Status: st}, nil
}

func (d *prgDeleter) deletedIDs() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]int64(nil), d.deleted...)
}

var prgNow = time.Date(2025, 3, 10, 12, 0, 0, 0, time.UTC)

// prgRow is a trashed row trashed age ago.
func prgRow(id int64, age time.Duration) domain.TrashedPost {
	return domain.TrashedPost{Post: domain.Post{ID: id, Status: "trash", Type: "post"}, TrashedAt: prgNow.Add(-age), HasTrashedAt: true}
}

const prgDay = 24 * time.Hour

// prgScheduler builds a scheduler over the fakes with the fake clock.
func prgScheduler(l *prgLister, d *prgDeleter, clk *prgClock, days int, types []string, log *slog.Logger, opts ...Option) *Scheduler {
	all := append([]Option{WithTrashPurge(l, d, days, types, time.Hour), withClock(clk.Now)}, opts...)
	return New(&fakeFinder{}, &fakePostUpdater{}, time.Minute, log, all...)
}

func TestPurgeCadenceFirstTickThenHourly(t *testing.T) {
	l := &prgLister{}
	d := &prgDeleter{}
	clk := &prgClock{now: prgNow}
	s := prgScheduler(l, d, clk, 30, []string{"post"}, testLogger())
	ctx := context.Background()

	s.tick(ctx)
	if got := l.callCount(); got == 0 {
		t.Fatal("first tick did not list trashed posts")
	}
	first := l.callCount()
	s.tick(ctx)
	clk.Advance(59 * time.Minute)
	s.tick(ctx)
	if got := l.callCount(); got != first {
		t.Fatalf("lister calls within the hour = %d, want %d", got, first)
	}
	clk.Advance(2 * time.Minute)
	s.tick(ctx)
	if got := l.callCount(); got <= first {
		t.Fatalf("tick after the hour did not purge (calls = %d)", got)
	}
}

func TestPurgeOnlyRowsOlderThanCutoff(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{
		prgRow(1, 8*prgDay),
		prgRow(2, 6*prgDay),
		prgRow(3, 7*prgDay), // exactly at the cutoff stays
		prgRow(4, 30*prgDay),
	}}
	d := &prgDeleter{}
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, testLogger())
	s.tick(context.Background())
	if got, want := d.deletedIDs(), []int64{1, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %v, want %v", got, want)
	}
}

func TestPurgeKeepsRowWithoutTrashTime(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{
		{Post: domain.Post{ID: 1, Status: "trash"}},
		prgRow(2, 90*prgDay),
	}}
	d := &prgDeleter{}
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, testLogger())
	s.tick(context.Background())
	if got, want := d.deletedIDs(), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %v, want %v", got, want)
	}
}

func TestPurgePassesTypesToLister(t *testing.T) {
	l := &prgLister{}
	types := []string{"post", "page", "product"}
	s := prgScheduler(l, &prgDeleter{}, &prgClock{now: prgNow}, 7, types, testLogger())
	s.tick(context.Background())
	if l.callCount() == 0 {
		t.Fatal("lister not called")
	}
	for _, c := range l.calls {
		if !reflect.DeepEqual(c.types, types) {
			t.Fatalf("types = %v, want %v", c.types, types)
		}
	}
}

func TestPurgeWalksEveryBatchByCursor(t *testing.T) {
	var rows []domain.TrashedPost
	for i := int64(1); i <= 250; i++ {
		rows = append(rows, prgRow(i, 40*prgDay))
	}
	l := &prgLister{rows: rows}
	d := &prgDeleter{}
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, testLogger())
	s.tick(context.Background())
	if got := len(d.deletedIDs()); got != 250 {
		t.Fatalf("deleted %d rows, want 250", got)
	}
	var afters []int64
	for _, c := range l.calls {
		if c.limit != 100 {
			t.Fatalf("limit = %d, want 100", c.limit)
		}
		afters = append(afters, c.after)
	}
	if len(afters) < 3 || afters[0] != 0 || afters[1] != 100 || afters[2] != 200 {
		t.Fatalf("cursors = %v, want 0,100,200,...", afters)
	}
}

func TestPurgeDaysZeroNeverLists(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{prgRow(1, 400*prgDay)}}
	d := &prgDeleter{}
	clk := &prgClock{now: prgNow}
	s := prgScheduler(l, d, clk, 0, []string{"post"}, testLogger())
	s.tick(context.Background())
	clk.Advance(3 * time.Hour)
	s.tick(context.Background())
	if l.callCount() != 0 || len(d.deletedIDs()) != 0 {
		t.Fatalf("days 0 listed %d times and deleted %v", l.callCount(), d.deletedIDs())
	}
}

func TestPurgeFailingDeleteIsLoggedAndNextRowProcessed(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{prgRow(1, 40*prgDay), prgRow(2, 40*prgDay), prgRow(3, 40*prgDay)}}
	d := &prgDeleter{failWith: map[int64]error{2: errors.New("boom")}}
	log, records := newCaptureLogger()
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, log)
	s.tick(context.Background())
	if got, want := d.deletedIDs(), []int64{1, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %v, want %v", got, want)
	}
	errs := 0
	for _, r := range records() {
		if r.level == slog.LevelError {
			errs++
		}
	}
	if errs != 1 {
		t.Fatalf("error records = %d, want 1", errs)
	}
}

func TestPurgeForbiddenIsDebugNotError(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{prgRow(1, 40*prgDay), prgRow(2, 40*prgDay)}}
	d := &prgDeleter{failWith: map[int64]error{1: fmt.Errorf("delete: %w", content.ErrForbidden)}}
	log, records := newCaptureLogger()
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, log)
	s.tick(context.Background())
	if got, want := d.deletedIDs(), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %v, want %v", got, want)
	}
	debug := 0
	for _, r := range records() {
		if r.level >= slog.LevelWarn {
			t.Fatalf("unexpected %v record %q for ErrForbidden", r.level, r.msg)
		}
		if r.level == slog.LevelDebug {
			debug++
		}
	}
	if debug == 0 {
		t.Fatal("ErrForbidden was not logged at debug")
	}
}

func TestPurgeSkipsRowNoLongerTrashed(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{prgRow(1, 40*prgDay), prgRow(2, 40*prgDay)}}
	d := &prgDeleter{status: map[int64]string{1: "publish"}}
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post"}, testLogger())
	s.tick(context.Background())
	if got, want := d.deletedIDs(), []int64{2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("deleted = %v, want %v (a restored row must survive)", got, want)
	}
}

func TestPurgeInfoLogOnlyWhenSomethingPurged(t *testing.T) {
	const msg = "scheduler: purged trashed posts"
	infoCount := func(rs []logRecord) (n int, count any) {
		for _, r := range rs {
			if r.msg == msg {
				if r.level != slog.LevelInfo {
					t.Fatalf("%q logged at %v, want info", msg, r.level)
				}
				n++
				count = r.attrs["count"]
			}
		}
		return n, count
	}

	log, records := newCaptureLogger()
	s := prgScheduler(&prgLister{rows: []domain.TrashedPost{prgRow(1, 2*prgDay)}}, &prgDeleter{},
		&prgClock{now: prgNow}, 7, []string{"post"}, log)
	s.tick(context.Background())
	if n, _ := infoCount(records()); n != 0 {
		t.Fatalf("info log present with nothing purged")
	}

	log, records = newCaptureLogger()
	s = prgScheduler(&prgLister{rows: []domain.TrashedPost{prgRow(1, 40*prgDay), prgRow(2, 40*prgDay)}}, &prgDeleter{},
		&prgClock{now: prgNow}, 7, []string{"post"}, log)
	s.tick(context.Background())
	n, count := infoCount(records())
	if n != 1 || fmt.Sprint(count) != "2" {
		t.Fatalf("info log count = %d with count attr %v, want one record with count 2", n, count)
	}
}

func TestPurgeUsesSchedulerPrincipalWithDeleteCapabilities(t *testing.T) {
	l := &prgLister{rows: []domain.TrashedPost{prgRow(1, 40*prgDay)}}
	d := &prgDeleter{}
	s := prgScheduler(l, d, &prgClock{now: prgNow}, 7, []string{"post", "product"}, testLogger(),
		WithDeleteCapabilitySuffixes("products"))
	s.tick(context.Background())
	if len(d.actors) != 1 {
		t.Fatalf("Delete calls = %d, want 1", len(d.actors))
	}
	actor := d.actors[0]
	for _, suffix := range []string{"posts", "pages", "products"} {
		for _, verb := range []string{"delete_", "delete_others_", "delete_published_", "delete_private_"} {
			if !actor.Can(verb + suffix) {
				t.Errorf("principal lacks %s%s", verb, suffix)
			}
		}
	}
	for _, c := range []string{"delete_users", "delete_plugins", "delete_themes", "manage_options", "delete_things"} {
		if actor.Can(c) {
			t.Errorf("principal holds %s", c)
		}
	}
}

func TestPurgeDeleteSuffixesDoNotChangePublishCapabilities(t *testing.T) {
	s := New(&fakeFinder{}, &fakePostUpdater{}, time.Minute, testLogger(), WithDeleteCapabilitySuffixes("products"))
	for _, c := range []string{"publish_pages", "edit_pages", "publish_products", "edit_products"} {
		if s.principal.Can(c) {
			t.Errorf("WithDeleteCapabilitySuffixes granted %s", c)
		}
	}
	for _, c := range []string{"publish_posts", "edit_others_posts", "edit_posts", "edit_published_posts"} {
		if !s.principal.Can(c) {
			t.Errorf("principal lost %s", c)
		}
	}
}

// Task 2.1 (M23 PR 1): the purge cadence is the configured interval, not the
// old fixed one-hour constant. These fail to compile until WithTrashPurge takes
// the interval as a fifth argument.

func TestTrashPurgeHonoursConfiguredInterval(t *testing.T) {
	const interval = 5 * time.Minute
	l := &prgLister{}
	clk := &prgClock{now: prgNow}
	s := New(&fakeFinder{}, &fakePostUpdater{}, time.Minute, testLogger(),
		WithTrashPurge(l, &prgDeleter{}, 30, []string{"post"}, interval), withClock(clk.Now))
	ctx := context.Background()

	s.tick(ctx)
	first := l.callCount()
	if first == 0 {
		t.Fatal("first tick did not scan (no prior scan)")
	}

	clk.Advance(2 * time.Minute)
	s.tick(ctx)
	if got := l.callCount(); got != first {
		t.Fatalf("scan ran 2m after the last one with a 5m interval (calls = %d, want %d)", got, first)
	}

	clk.Advance(4 * time.Minute) // 6m since the last scan
	s.tick(ctx)
	second := l.callCount()
	if second <= first {
		t.Fatalf("scan did not run once the 5m interval elapsed (calls = %d)", second)
	}

	clk.Advance(4*time.Minute + 59*time.Second)
	s.tick(ctx)
	if got := l.callCount(); got != second {
		t.Fatalf("scan ran 4m59s after the last one (calls = %d, want %d)", got, second)
	}

	clk.Advance(time.Second) // exactly 5m: elapsed >= interval scans
	s.tick(ctx)
	if got := l.callCount(); got <= second {
		t.Fatalf("scan did not run at exactly the interval (calls = %d)", got)
	}
}

// Feature: media-delete-and-purge-config, Property 5: The trash purge scan respects the configured interval
// Validates: Requirements 8.1, 8.2, 8.3, 8.4
func TestPropertyTrashPurgeRespectsInterval(t *testing.T) {
	prop := func(intervalSecs uint16, gapSecs []uint16) bool {
		interval := time.Duration(int(intervalSecs)%7200+1) * time.Second
		l := &prgLister{}
		clk := &prgClock{now: prgNow}
		s := New(&fakeFinder{}, &fakePostUpdater{}, time.Minute, testLogger(),
			WithTrashPurge(l, &prgDeleter{}, 30, []string{"post"}, interval), withClock(clk.Now))
		ctx := context.Background()

		var lastScan time.Time
		scanned := false
		for i := 0; i <= len(gapSecs); i++ {
			if i > 0 {
				// Strictly increasing timestamps: every gap is at least one second.
				clk.Advance(time.Duration(int(gapSecs[i-1])%14400+1) * time.Second)
			}
			now := clk.Now()
			want := !scanned || now.Sub(lastScan) >= interval
			before := l.callCount()
			s.tick(ctx)
			if got := l.callCount() > before; got != want {
				return false
			}
			if want {
				scanned, lastScan = true, now
			}
		}
		return true
	}
	cfg := &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(23))}
	if err := quick.Check(prop, cfg); err != nil {
		t.Fatal(err)
	}
}
