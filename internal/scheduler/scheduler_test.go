package scheduler

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// fakeFinder is a domain.ScheduledPostFinder test double that always
// returns the same fixed set of due posts, recording every asOf it was
// called with.
type fakeFinder struct {
	mu    sync.Mutex
	due   []domain.Post
	calls []time.Time
}

func (f *fakeFinder) DueScheduled(_ context.Context, asOf time.Time) ([]domain.Post, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, asOf)
	out := make([]domain.Post, len(f.due))
	copy(out, f.due)
	return out, nil
}

func (f *fakeFinder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// updateCall records a single invocation of the postUpdater the Scheduler
// was given.
type updateCall struct {
	actor            auth.Principal
	post             domain.Post
	expectedModified time.Time
}

// fakePostUpdater is a postUpdater test double. failIDs names post IDs
// whose Update call should return an error, so tests can exercise Req 4.7
// (one failure does not abort the rest of the batch).
type fakePostUpdater struct {
	mu      sync.Mutex
	calls   []updateCall
	failIDs map[int64]bool
}

func (f *fakePostUpdater) Update(_ context.Context, actor auth.Principal, p domain.Post, expectedModified time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, updateCall{actor: actor, post: p, expectedModified: expectedModified})
	if f.failIDs != nil && f.failIDs[p.ID] {
		return errors.New("boom")
	}
	return nil
}

func (f *fakePostUpdater) snapshot() []updateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]updateCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestSchedulerRunUpdatesEachDuePostOncePerTick(t *testing.T) {
	finder := &fakeFinder{due: []domain.Post{
		{ID: 10, Status: "future"},
		{ID: 20, Status: "future"},
	}}
	updater := &fakePostUpdater{}
	s := New(finder, updater, 5*time.Millisecond, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Give it long enough to complete at least one tick.
	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	calls := updater.snapshot()
	seen := map[int64]int{}
	for _, c := range calls {
		seen[c.post.ID]++
		if c.actor.UserID != systemPrincipal.UserID || c.actor.Login != systemPrincipal.Login {
			t.Fatalf("Update called with actor %+v, want systemPrincipal %+v", c.actor, systemPrincipal)
		}
	}
	if seen[10] == 0 {
		t.Fatalf("post 10 was never updated; calls=%+v", calls)
	}
	if seen[20] == 0 {
		t.Fatalf("post 20 was never updated; calls=%+v", calls)
	}
}

func TestSchedulerRunContinuesAfterOneUpdateFails(t *testing.T) {
	finder := &fakeFinder{due: []domain.Post{
		{ID: 10, Status: "future"},
		{ID: 20, Status: "future"},
	}}
	updater := &fakePostUpdater{failIDs: map[int64]bool{10: true}}
	s := New(finder, updater, 5*time.Millisecond, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	seen := map[int64]int{}
	for _, c := range updater.snapshot() {
		seen[c.post.ID]++
	}
	if seen[10] == 0 {
		t.Fatal("post 10 (the failing update) should still have been attempted")
	}
	if seen[20] == 0 {
		t.Fatal("post 20 should still have been updated despite post 10's failure (Req 4.7)")
	}
}

func TestSchedulerRunReturnsPromptlyWhenContextCancelled(t *testing.T) {
	finder := &fakeFinder{}
	updater := &fakePostUpdater{}
	s := New(finder, updater, time.Hour, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Cancel almost immediately; Run must not block for the full 1h
	// interval waiting for its next tick.
	time.Sleep(5 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run blocked past context cancellation instead of returning promptly")
	}
}

func TestSchedulerRunPollsFinderOnEachTick(t *testing.T) {
	finder := &fakeFinder{due: nil}
	updater := &fakePostUpdater{}
	s := New(finder, updater, 5*time.Millisecond, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	if finder.callCount() == 0 {
		t.Fatal("expected DueScheduled to have been polled at least once")
	}
}
