package content

import (
	"context"

	"github.com/roboweaver/grimoire/internal/domain"
)

type NavMenuService struct {
	repo  domain.NavMenuRepository
	theme string
}

func NewNavMenuService(repo domain.NavMenuRepository, theme string) *NavMenuService {
	return &NavMenuService{repo: repo, theme: theme}
}

func (s *NavMenuService) List(ctx context.Context) ([]domain.NavMenu, error) {
	return s.repo.Menus(ctx)
}

func (s *NavMenuService) ByLocation(ctx context.Context, location string) (domain.NavMenu, error) {
	return s.repo.MenuByLocation(ctx, s.theme, location)
}

func (s *NavMenuService) BySlug(ctx context.Context, slug string) (domain.NavMenu, error) {
	return s.repo.MenuBySlug(ctx, slug)
}

// Get returns a single menu (with its item tree) by id. MenuByID degrades to a
// zero-value NavMenu{} with a nil error for an unknown id, so Get translates
// that into domain.ErrNotFound itself (Req 7's GET /admin/api/menus/{id}
// needs a real 404 on a missing/unknown menu).
func (s *NavMenuService) Get(ctx context.Context, id int64) (domain.NavMenu, error) {
	menu, err := s.repo.MenuByID(ctx, id)
	if err != nil {
		return domain.NavMenu{}, err
	}
	if menu.ID == 0 {
		return domain.NavMenu{}, domain.ErrNotFound
	}
	return menu, nil
}
