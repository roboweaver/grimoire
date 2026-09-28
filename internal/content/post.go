package content

import (
	"context"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// PostService orchestrates post/page reads for the web layer.
type PostService struct {
	posts domain.PostRepository
	pc    domain.PostCounter // optional; set via WithCounter for RecentPage
	// users is optional; set via WithAuthors for AuthorArchive.
	users domain.UserRepository
}

// NewPostService constructs a PostService over a PostRepository. Unchanged
// signature -- all 20 existing call sites are untouched.
func NewPostService(p domain.PostRepository) *PostService {
	return &PostService{posts: p}
}

// WithCounter opts a PostService into RecentPage support by attaching a
// PostCounter. Returns the receiver so it can be chained at construction time
// (e.g. content.NewPostService(repos.Posts).WithCounter(repos.PostCounter)).
func (s *PostService) WithCounter(pc domain.PostCounter) *PostService {
	s.pc = pc
	return s
}

// WithAuthors opts a PostService into AuthorArchive support by attaching the
// user read its nicename resolution needs. Returns the receiver so it can be
// chained at construction time (e.g.
// content.NewPostService(repos.Posts).WithCounter(repos.PostCounter).WithAuthors(repos.Users)),
// following WithCounter rather than changing a constructor signature.
//
// DateArchive needs nothing extra, so it is available on every PostService.
func (s *PostService) WithAuthors(u domain.UserRepository) *PostService {
	s.users = u
	return s
}

// Recent returns published posts for a 1-based page, clamping the page size to
// [1, MaxPerPage] with DefaultPerPage when unset. Unchanged behavior.
func (s *PostService) Recent(ctx context.Context, page, perPage int) ([]domain.Post, error) {
	limit, offset, _ := clamp(page, perPage)
	return s.posts.RecentPosts(ctx, limit, offset)
}

// RecentPage is Recent plus a Page pagination contract (Req 8.1), for callers
// that need total/out-of-range information (the public home page). Requires
// WithCounter to have been called; panics on a nil pc, matching Go's normal
// nil-pointer-dereference behavior for an unwired dependency rather than
// silently returning a zero Page.
func (s *PostService) RecentPage(ctx context.Context, page, perPage int) ([]domain.Post, Page, error) {
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.RecentPosts(ctx, limit, offset)
	if err != nil {
		return nil, Page{}, err
	}
	total, err := s.pc.CountByStatus(ctx, "post", "publish")
	if err != nil {
		return nil, Page{}, err
	}
	return posts, newPage(clampedPage, limit, total), nil
}

// BySlug resolves a single published post or page by slug. domain.ErrNotFound
// is propagated for unknown or non-published slugs.
func (s *PostService) BySlug(ctx context.Context, slug string) (domain.Post, error) {
	return s.posts.BySlug(ctx, slug, "post", "page")
}

// PublishedByID resolves a single published post or page by primary key,
// mirroring BySlug's published-only, post-or-page semantics.
// domain.ErrNotFound is propagated for unknown or non-published ids.
//
// This backs the %post_id% permalink token, where the id arrives from a
// visitor's URL and is therefore trivially guessable. It deliberately does not
// reach the status-blind write-side lookup of the same row, which would disclose
// drafts, private and trashed posts.
func (s *PostService) PublishedByID(ctx context.Context, id int64) (domain.Post, error) {
	return s.posts.PublishedByID(ctx, id, "post", "page")
}

// HasAuthors reports whether WithAuthors supplied the user read AuthorArchive
// needs.
//
// It is TermService.HasHierarchy's twin and exists for the same reason: the
// author archive route is registered unconditionally from the permalink
// structure, so a router needs to be able to tell the wiring gap from a working
// server before it accepts traffic, and AuthorArchive's deliberate nil
// dereference cannot tell it. Deliberately not paired with a HasCounter: nothing
// registers RecentPage's route without also opting into it.
func (s *PostService) HasAuthors() bool {
	return s.users != nil
}

// AuthorArchive resolves an author by user_nicename and returns that author's
// page of published posts (Req 7.1).
//
// It lives on PostService rather than on TermService because an author archive
// is a post listing with one extra predicate — ArchiveFilter.AuthorID — and no
// taxonomy read at all. The returned Archive therefore carries a zero Term and
// a nil Ancestry, and its pagination comes from the same clamp/newPage helpers
// every other paginated read uses, so there is exactly one Page shape (Req 8.1).
//
// The filter selects by the resolved user's ID, not by the nicename: the
// nicename is a URL component, post_author is a foreign key. Resolution is one
// ByNicename read per request, whose lowest-ID-wins rule (Req 7.6) is what makes
// a duplicated nicename pick the same author on all three vendors.
//
// Heading is the author's display_name, and it is the only place the author
// appears in the result: Archive carries no user field, so user_login is not
// rendered and the archive publishes no login name the site had not already
// exposed (Req 7.4).
//
// domain.ErrNotFound is returned, before any post query, when the nicename names
// no user (Req 7.3) — the same not-found-skips-query behavior CategoryArchive and
// TagArchive have, so a 404 costs no listing query and no count query. An author
// who exists but has published nothing is an empty archive with a zero Total,
// not a not-found: the user resolved, so nothing is absent.
//
// Requires WithAuthors to have been called; panics on a nil reader, matching
// RecentPage's handling of an unwired PostCounter and CategoryArchive's of an
// unwired TermReader — Go's normal nil-dereference behavior for a dependency
// that was never supplied, rather than a zero Archive that looks like an author
// with no posts.
func (s *PostService) AuthorArchive(ctx context.Context, nicename string, page, perPage int) (Archive, error) {
	user, err := s.users.ByNicename(ctx, nicename)
	if err != nil {
		return Archive{}, err
	}
	filter := domain.ArchiveFilter{AuthorID: user.ID}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.PublishedArchive(ctx, filter, limit, offset)
	if err != nil {
		return Archive{}, err
	}
	total, err := s.posts.CountPublishedArchive(ctx, filter)
	if err != nil {
		return Archive{}, err
	}
	return Archive{
		Heading: user.DisplayName,
		Posts:   posts,
		Page:    newPage(clampedPage, limit, total),
	}, nil
}

// DateArchive returns the page of published posts a date archive covers, at
// whichever granularity the DateRef carries (Req 6.1).
//
// The interval is the half-open [Start, End) range routing.DateRef.Range builds,
// passed into both the listing and the count unchanged. It is deliberately not
// re-derived here: Range establishes the UTC basis that makes wprepo's formatTS
// the identity on these bounds (Req 6.4), and a second construction site could
// drift from it by a host offset — which would look like plausible output and be
// reported months later. Passing one interval to both reads is also what keeps
// Page.Total describing the rows actually listed.
//
// Heading is formatted here rather than in the template, per granularity: 2024,
// May 2024, 17 May 2024. Like the author archive this carries no term, so Term
// is the zero value and Ancestry is nil, and pagination comes from the shared
// clamp/newPage helpers (Req 8.1).
//
// domain.ErrNotFound is returned, before any query, when the components do not
// form a real calendar date (Req 6.3) — 2024/02/30, 2024/13/01, a day without a
// month, a missing year. Classify rejects those paths before a handler is
// reached, so this is a second line of defence, and it is what makes "404 rather
// than querying" true of the service and not only of the router. A *real* date
// matching no post is the opposite case: an empty archive with a zero Total
// rather than a not-found (Req 6.5), because there is no entity here to be
// absent — a month either contains posts or does not.
func (s *PostService) DateArchive(ctx context.Context, d routing.DateRef, page, perPage int) (Archive, error) {
	start, end, ok := d.Range()
	if !ok {
		return Archive{}, domain.ErrNotFound
	}
	filter := domain.ArchiveFilter{Start: start, End: end}
	limit, offset, clampedPage := clamp(page, perPage)
	posts, err := s.posts.PublishedArchive(ctx, filter, limit, offset)
	if err != nil {
		return Archive{}, err
	}
	total, err := s.posts.CountPublishedArchive(ctx, filter)
	if err != nil {
		return Archive{}, err
	}
	return Archive{
		Heading: dateHeading(d, start),
		Posts:   posts,
		Page:    newPage(clampedPage, limit, total),
	}, nil
}

// dateHeading renders a date archive's heading at the granularity d carries.
// start is the interval's lower bound, so the month and day names come from the
// same instant the query selects on rather than from a second time.Date call.
func dateHeading(d routing.DateRef, start time.Time) string {
	switch {
	case d.Month == 0:
		return start.Format("2006")
	case d.Day == 0:
		return start.Format("January 2006")
	default:
		return start.Format("2 January 2006")
	}
}
