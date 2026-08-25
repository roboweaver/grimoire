// Package scheduler polls for posts due to publish and flips them from
// "future" to "publish" through the same write path (content.PostWriteService.
// Update) as every other post update, so publish authorization/business
// rules live in exactly one place (Requirement 4.4).
package scheduler

import (
	"context"
	"log/slog"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// postUpdater is the narrow slice of content.PostWriteService's API the
// scheduler needs. *content.PostWriteService satisfies it; tests substitute
// a fake so the scheduler package has no import of internal/content (and,
// transitively, none of internal/web).
type postUpdater interface {
	Update(ctx context.Context, actor auth.Principal, p domain.Post, expectedModified time.Time) error
}

// Scheduler polls for posts due to publish and flips them, on a ticker. It
// is started and stopped by cmd/grimoire/main.go using the same
// context/lifecycle main.go already uses for its HTTP server goroutine --
// there is no separate scheduler lifecycle to reason about (Requirement 4.3).
type Scheduler struct {
	finder   domain.ScheduledPostFinder
	posts    postUpdater
	interval time.Duration
	log      *slog.Logger
}

// New constructs a Scheduler.
func New(finder domain.ScheduledPostFinder, posts postUpdater, interval time.Duration, log *slog.Logger) *Scheduler {
	return &Scheduler{finder: finder, posts: posts, interval: interval, log: log}
}

// systemPrincipal is unexported: constructed once inside this package,
// never returned, never accepted as a parameter from outside the package.
// It cannot be obtained via any HTTP route, session, or Application
// Password (Requirement 4.5).
var systemPrincipal = auth.Principal{
	UserID: 0,
	Login:  "grimoire-scheduler",
	Caps:   map[string]bool{"publish_posts": true, "edit_others_posts": true, "edit_posts": true},
}

// Run blocks, ticking every s.interval, until ctx is cancelled. main.go
// calls this in its own goroutine, exactly like srv.ListenAndServe().
func (s *Scheduler) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

// tick performs a single check-and-publish pass: it looks up every post due
// to publish and transitions each one independently, so one post's failure
// (e.g. a concurrent edit already moved it out of "future") does not abort
// the rest of the batch (Requirement 4.7).
func (s *Scheduler) tick(ctx context.Context) {
	due, err := s.finder.DueScheduled(ctx, time.Now())
	if err != nil {
		if s.log != nil {
			s.log.Error("scheduler: DueScheduled failed", "error", err)
		}
		return
	}
	for _, p := range due {
		p.Status = "publish"
		// A zero expectedModified skips the optimistic-concurrency check
		// PostWriteService.Update otherwise enforces: the scheduler has no
		// prior "loaded at" timestamp to compare against, and Requirement
		// 4.7 requires that a concurrently-edited post simply be skipped,
		// not treated as a conflict error that aborts the whole tick.
		if err := s.posts.Update(ctx, systemPrincipal, p, time.Time{}); err != nil {
			if s.log != nil {
				s.log.Error("scheduler: failed to publish scheduled post", "post_id", p.ID, "error", err)
			}
			continue
		}
	}
}
