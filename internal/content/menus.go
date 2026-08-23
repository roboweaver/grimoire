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
