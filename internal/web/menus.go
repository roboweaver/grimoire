package web

import (
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
)

func navMenuView(menu domain.NavMenu) render.NavMenuView {
	out := render.NavMenuView{Name: menu.Name}
	for _, item := range menu.Items {
		out.Items = append(out.Items, navMenuItemView(item))
	}
	return out
}

func navMenuItemView(item domain.NavMenuItem) render.NavMenuItemView {
	out := render.NavMenuItemView{Label: item.Label, URL: item.URL}
	for _, child := range item.Children {
		out.Children = append(out.Children, navMenuItemView(child))
	}
	return out
}
