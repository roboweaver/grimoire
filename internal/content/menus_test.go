package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeNavMenuRepo struct {
	menus         []domain.NavMenu
	byLocation    map[string]domain.NavMenu
	bySlug        map[string]domain.NavMenu
	menusErr      error
	byLocationErr map[string]error
	bySlugErr     map[string]error
	lastTheme     string
	lastLocation  string
	lastSlug      string
}

func (f *fakeNavMenuRepo) Menus(context.Context) ([]domain.NavMenu, error) {
	if f.menusErr != nil {
		return nil, f.menusErr
	}
	return append([]domain.NavMenu(nil), f.menus...), nil
}

func (f *fakeNavMenuRepo) MenuBySlug(_ context.Context, slug string) (domain.NavMenu, error) {
	f.lastSlug = slug
	if err := f.bySlugErr[slug]; err != nil {
		return domain.NavMenu{}, err
	}
	return f.bySlug[slug], nil
}

func (f *fakeNavMenuRepo) MenuByID(context.Context, int64) (domain.NavMenu, error) {
	return domain.NavMenu{}, nil
}

func (f *fakeNavMenuRepo) MenuByLocation(_ context.Context, theme, location string) (domain.NavMenu, error) {
	f.lastTheme = theme
	f.lastLocation = location
	if err := f.byLocationErr[location]; err != nil {
		return domain.NavMenu{}, err
	}
	return f.byLocation[location], nil
}

func TestNavMenuServiceResolvesLocationAndSlug(t *testing.T) {
	repo := &fakeNavMenuRepo{
		menus:      []domain.NavMenu{{ID: 1, Name: "Primary"}},
		byLocation: map[string]domain.NavMenu{"primary": {ID: 1, Name: "Primary"}},
		bySlug:     map[string]domain.NavMenu{"footer": {ID: 2, Name: "Footer"}},
	}
	svc := NewNavMenuService(repo, "twentytwentysix")
	menus, err := svc.List(context.Background())
	if err != nil || len(menus) != 1 {
		t.Fatalf("List = %v %v", menus, err)
	}
	menu, err := svc.ByLocation(context.Background(), "primary")
	if err != nil || menu.ID != 1 {
		t.Fatalf("ByLocation = %+v %v", menu, err)
	}
	if repo.lastTheme != "twentytwentysix" || repo.lastLocation != "primary" {
		t.Fatalf("location lookup = theme %q location %q", repo.lastTheme, repo.lastLocation)
	}
	menu, err = svc.BySlug(context.Background(), "footer")
	if err != nil || menu.ID != 2 {
		t.Fatalf("BySlug = %+v %v", menu, err)
	}
	if repo.lastSlug != "footer" {
		t.Fatalf("slug lookup = %q", repo.lastSlug)
	}
}
