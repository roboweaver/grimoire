package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

// postByID reads a single post/page by primary key regardless of status or
// type. domain.PostWriter satisfies it; the admin detail endpoint reuses that
// reader so no new port is required.
type postByID interface {
	ByID(ctx context.Context, id int64) (domain.Post, error)
}

// userByID reads a single user by primary key. domain.UserRepository satisfies
// it; the admin service uses it only to resolve display names.
type userByID interface {
	ByID(ctx context.Context, id int64) (domain.User, error)
}

// Stats holds the dashboard counts returned by the admin API. All values are
// derived from pure COUNT(*) reads.
type Stats struct {
	PostsPublished int
	PostsDraft     int
	Pages          int
	Categories     int
	Users          int
}

// AdminList is a page of admin content plus pagination metadata.
type AdminList struct {
	Items      []domain.Post
	Page       int
	PerPage    int
	Total      int
	TotalPages int
}

// AdminService provides the read-only aggregations backing the M3 admin API. It
// composes the additive count/list ports with the existing post and user
// readers. Every method is a pure read; the service performs no writes and no
// authorization (the web layer gates access with the edit_posts capability).
type AdminService struct {
	posts  domain.AdminPostRepository
	detail postByID
	pc     domain.PostCounter
	uc     domain.UserCounter
	tc     domain.TermCounter
	users  userByID
}

// NewAdminService constructs an AdminService from the admin list/count ports, a
// post-by-ID reader (typically the PostWriter), and a user-by-ID reader.
func NewAdminService(
	posts domain.AdminPostRepository,
	detail postByID,
	pc domain.PostCounter,
	uc domain.UserCounter,
	tc domain.TermCounter,
	users userByID,
) *AdminService {
	return &AdminService{posts: posts, detail: detail, pc: pc, uc: uc, tc: tc, users: users}
}

// List returns a page of content (posts and pages, including drafts) with
// pagination metadata. page is 1-based; perPage is clamped to [1, MaxPerPage]
// with DefaultPerPage when unset. Non-empty typ/status become single-element
// filters; empty values are left unset so the repository applies its defaults
// (both post and page; all statuses). The total ignores paging.
func (s *AdminService) List(ctx context.Context, page, perPage int, typ, status string) (AdminList, error) {
	limit, offset, page := clamp(page, perPage)
	f := domain.AdminPostFilter{Limit: limit, Offset: offset}
	if typ != "" {
		f.Types = []string{typ}
	}
	if status != "" {
		f.Statuses = []string{status}
	}
	items, err := s.posts.ListForAdmin(ctx, f)
	if err != nil {
		return AdminList{}, err
	}
	total, err := s.posts.CountForAdmin(ctx, domain.AdminPostFilter{Types: f.Types, Statuses: f.Statuses})
	if err != nil {
		return AdminList{}, err
	}
	totalPages := 0
	if limit > 0 {
		totalPages = (total + limit - 1) / limit
	}
	return AdminList{
		Items:      items,
		Page:       page,
		PerPage:    limit,
		Total:      total,
		TotalPages: totalPages,
	}, nil
}

// Detail returns a single post/page by ID regardless of status or type.
// domain.ErrNotFound is propagated for a missing record.
func (s *AdminService) Detail(ctx context.Context, id int64) (domain.Post, error) {
	return s.detail.ByID(ctx, id)
}

// Stats aggregates the dashboard counts: published and draft posts, pages of any
// status, category terms, and users.
func (s *AdminService) Stats(ctx context.Context) (Stats, error) {
	published, err := s.pc.CountByStatus(ctx, "post", "publish")
	if err != nil {
		return Stats{}, err
	}
	draft, err := s.pc.CountByStatus(ctx, "post", "draft")
	if err != nil {
		return Stats{}, err
	}
	pages, err := s.pc.CountByStatus(ctx, "page", "")
	if err != nil {
		return Stats{}, err
	}
	categories, err := s.tc.CountTerms(ctx, "category")
	if err != nil {
		return Stats{}, err
	}
	users, err := s.uc.CountUsers(ctx)
	if err != nil {
		return Stats{}, err
	}
	return Stats{
		PostsPublished: published,
		PostsDraft:     draft,
		Pages:          pages,
		Categories:     categories,
		Users:          users,
	}, nil
}

// DisplayName resolves a user's display name by ID. It is used to enrich the
// session endpoint, whose principal carries only the login. domain.ErrNotFound
// is propagated for a missing user.
func (s *AdminService) DisplayName(ctx context.Context, userID int64) (string, error) {
	u, err := s.users.ByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return u.DisplayName, nil
}
