package storagetest

import (
	"context"
	"testing"
)

func runMenusContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("NavMenuRepository menus by slug by id and by location", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		menus, err := repos.NavMenus.Menus(ctx)
		if err != nil {
			t.Fatalf("Menus: %v", err)
		}
		if len(menus) != 1 || menus[0].Slug != "primary" {
			t.Fatalf("menus = %+v, want [primary]", menus)
		}
		bySlug, err := repos.NavMenus.MenuBySlug(ctx, "primary")
		if err != nil {
			t.Fatalf("MenuBySlug: %v", err)
		}
		if len(bySlug.Items) != 3 {
			t.Fatalf("top-level items = %d, want 3", len(bySlug.Items))
		}
		if bySlug.Items[0].Label != "Home" || bySlug.Items[0].URL != "/home-custom" {
			t.Fatalf("custom item = %+v", bySlug.Items[0])
		}
		if bySlug.Items[1].Label != "About" || bySlug.Items[1].URL != "/about" {
			t.Fatalf("post_type item = %+v", bySlug.Items[1])
		}
		if bySlug.Items[2].Label != "News" || bySlug.Items[2].URL != "/category/news" {
			t.Fatalf("taxonomy item = %+v", bySlug.Items[2])
		}
		if len(bySlug.Items[0].Children) != 1 || bySlug.Items[0].Children[0].Label != "Sub Home" {
			t.Fatalf("children = %+v", bySlug.Items[0].Children)
		}
		byID, err := repos.NavMenus.MenuByID(ctx, 30)
		if err != nil {
			t.Fatalf("MenuByID: %v", err)
		}
		if byID.Slug != "primary" {
			t.Fatalf("MenuByID slug = %q, want primary", byID.Slug)
		}
		byLoc, err := repos.NavMenus.MenuByLocation(ctx, "twentytwentyfive", "primary")
		if err != nil {
			t.Fatalf("MenuByLocation: %v", err)
		}
		if byLoc.ID != 30 || byLoc.Items[1].URL != "/about" {
			t.Fatalf("MenuByLocation = %+v", byLoc)
		}
	})

	t.Run("NavMenuRepository empty degradation", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		missingSlug, err := repos.NavMenus.MenuBySlug(ctx, "missing")
		if err != nil {
			t.Fatalf("MenuBySlug missing: %v", err)
		}
		if missingSlug.ID != 0 || len(missingSlug.Items) != 0 {
			t.Fatalf("missing slug = %+v, want empty menu", missingSlug)
		}
		missingLoc, err := repos.NavMenus.MenuByLocation(ctx, "twentytwentyfive", "footer")
		if err != nil {
			t.Fatalf("MenuByLocation missing: %v", err)
		}
		if missingLoc.ID != 0 || len(missingLoc.Items) != 0 {
			t.Fatalf("missing location = %+v, want empty menu", missingLoc)
		}
	})
}
