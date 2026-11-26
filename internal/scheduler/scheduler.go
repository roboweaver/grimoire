// Package scheduler polls for posts due to publish and flips them from
// "future" to "publish" through the same write path (content.PostWriteService.
// Update) as every other post update, so publish authorization/business
// rules live in exactly one place (Requirement 4.4).
package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
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
	// principal is the acting identity for every publish; it starts as a copy
	// of systemPrincipal and gains capabilities per WithCapabilitySuffixes.
	principal auth.Principal

	// Trash purge (WithTrashPurge). Disabled while purgeDays is 0.
	trashed   domain.TrashedPostLister
	deleter   trashDeleter
	purgeDays int
	purgeTyps []string
	lastPurge time.Time
	now       func() time.Time
}

// purgeInterval is the minimum spacing between trash purges; purgeBatch is the
// page size of the id-cursor walk.
const (
	purgeInterval = time.Hour
	purgeBatch    = 100
)

// trashDeleter is the slice of content.PostWriteService the purge needs.
type trashDeleter interface {
	Delete(ctx context.Context, actor auth.Principal, p domain.Post) error
	ByID(ctx context.Context, id int64) (domain.Post, error)
}

// WithTrashPurge enables the hourly permanent deletion of rows trashed more
// than days days ago, among the given (managed) post types. days <= 0 disables it.
func WithTrashPurge(l domain.TrashedPostLister, d trashDeleter, days int, types []string) Option {
	return func(s *Scheduler) {
		s.trashed, s.deleter, s.purgeDays, s.purgeTyps = l, d, days, types
	}
}

// WithDeleteCapabilitySuffixes grants the scheduler's principal the four
// delete capabilities for posts, pages and each given suffix. It does not
// change the publish capabilities.
func WithDeleteCapabilitySuffixes(suffixes ...string) Option {
	return func(s *Scheduler) {
		all := append([]string{"posts", "pages"}, suffixes...)
		for _, suffix := range all {
			for _, verb := range []string{"delete_", "delete_others_", "delete_published_", "delete_private_"} {
				s.principal.Caps[verb+suffix] = true
			}
		}
	}
}

// withClock replaces the purge clock (tests only).
func withClock(now func() time.Time) Option {
	return func(s *Scheduler) { s.now = now }
}

// Option customizes a Scheduler at construction.
type Option func(*Scheduler)

// WithCapabilitySuffixes grants the scheduler's principal the four capabilities
// needed to publish a post whose type uses each capability suffix (for example
// "products" grants edit_products, edit_others_products,
// edit_published_products and publish_products). Without it the scheduler can
// only publish built-in post types.
func WithCapabilitySuffixes(suffixes ...string) Option {
	return func(s *Scheduler) {
		for _, suffix := range suffixes {
			for _, verb := range []string{"edit_", "edit_others_", "edit_published_", "publish_"} {
				s.principal.Caps[verb+suffix] = true
			}
		}
	}
}

// New constructs a Scheduler.
func New(finder domain.ScheduledPostFinder, posts postUpdater, interval time.Duration, log *slog.Logger, opts ...Option) *Scheduler {
	caps := make(map[string]bool, len(systemPrincipal.Caps))
	for k, v := range systemPrincipal.Caps {
		caps[k] = v
	}
	principal := systemPrincipal
	principal.Caps = caps
	s := &Scheduler{finder: finder, posts: posts, interval: interval, log: log, principal: principal, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// systemPrincipal is unexported: constructed once inside this package,
// never returned, never accepted as a parameter from outside the package.
// It cannot be obtained via any HTTP route, session, or Application
// Password (Requirement 4.5).
// edit_published_posts is required alongside publish_posts and
// edit_others_posts: PostWriteService.Update's auth.CanEditPost check
// evaluates the capability for the post's TARGET status when the status is
// changing, and every real WordPress role that carries publish_posts also
// carries edit_published_posts (see roles.go) -- so the scheduler needs it
// too to actually flip a "future" post to "publish" through that path.
var systemPrincipal = auth.Principal{
	UserID: 0,
	Login:  "grimoire-scheduler",
	Caps:   map[string]bool{"publish_posts": true, "edit_others_posts": true, "edit_posts": true, "edit_published_posts": true},
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
	defer s.purgeTrash(ctx)
	due, err := s.finder.DueScheduled(ctx, time.Now())
	if err != nil {
		if s.log != nil {
			s.log.Error("scheduler: DueScheduled failed", "error", err)
		}
		return
	}
	gate, gated := s.posts.(interface{ WritableType(string) bool })
	for _, p := range due {
		if gated && !gate.WritableType(p.Type) {
			if s.log != nil {
				s.log.Debug("scheduler: skipping post of unwritable type", "post_id", p.ID, "type", p.Type)
			}
			continue
		}
		p.Status = "publish"
		// A zero expectedModified skips the optimistic-concurrency check
		// PostWriteService.Update otherwise enforces: the scheduler has no
		// prior "loaded at" timestamp to compare against, and Requirement
		// 4.7 requires that a concurrently-edited post simply be skipped,
		// not treated as a conflict error that aborts the whole tick.
		if err := s.posts.Update(ctx, s.principal, p, time.Time{}); err != nil {
			if s.log != nil {
				s.log.Error("scheduler: failed to publish scheduled post", "post_id", p.ID, "error", err)
			}
			continue
		}
	}
}

// purgeTrash permanently deletes trashed rows older than the retention, at most
// once per purgeInterval. A row without a recorded trash time, or trashed
// exactly at the cutoff, stays. Failures are logged and the walk continues.
func (s *Scheduler) purgeTrash(ctx context.Context) {
	if s.purgeDays <= 0 || s.trashed == nil || s.deleter == nil {
		return
	}
	now := s.now()
	if !s.lastPurge.IsZero() && now.Sub(s.lastPurge) < purgeInterval {
		return
	}
	s.lastPurge = now
	cutoff := now.Add(-time.Duration(s.purgeDays) * 24 * time.Hour)
	var after int64
	purged := 0
	for ctx.Err() == nil {
		batch, err := s.trashed.TrashedPosts(ctx, s.purgeTyps, after, purgeBatch)
		if err != nil {
			s.logf(slog.LevelError, "scheduler: listing trashed posts failed", "error", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		for _, tp := range batch {
			after = tp.Post.ID
			if !tp.HasTrashedAt || !tp.TrashedAt.Before(cutoff) {
				continue
			}
			cur, err := s.deleter.ByID(ctx, tp.Post.ID)
			if err != nil {
				s.logPurgeErr(tp.Post.ID, err)
				continue
			}
			if cur.Status != "trash" {
				continue
			}
			if err := s.deleter.Delete(ctx, s.principal, cur); err != nil {
				s.logPurgeErr(tp.Post.ID, err)
				continue
			}
			purged++
		}
	}
	if purged > 0 {
		s.logf(slog.LevelInfo, "scheduler: purged trashed posts", "count", purged)
	}
}

func (s *Scheduler) logPurgeErr(id int64, err error) {
	if errors.Is(err, content.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
		s.logf(slog.LevelDebug, "scheduler: skipping trashed post", "post_id", id, "error", err)
		return
	}
	s.logf(slog.LevelError, "scheduler: failed to purge trashed post", "post_id", id, "error", err)
}

func (s *Scheduler) logf(level slog.Level, msg string, args ...any) {
	if s.log != nil {
		s.log.Log(context.Background(), level, msg, args...)
	}
}
