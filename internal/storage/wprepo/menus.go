package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

var _ domain.NavMenuRepository = (*NavMenuRepo)(nil)

type NavMenuRepo struct {
	db     *bun.DB
	prefix string
}

func NewNavMenuRepo(db *bun.DB, prefix string) *NavMenuRepo {
	return &NavMenuRepo{db: db, prefix: prefix}
}

type navMenuRow struct {
	ID   int64  `bun:"term_id"`
	Name string `bun:"name"`
	Slug string `bun:"slug"`
}

type navItemRow struct {
	ID       int64  `bun:"ID"`
	Title    string `bun:"post_title"`
	Order    int    `bun:"menu_order"`
	Type     string `bun:"item_type"`
	Object   string `bun:"item_object"`
	ObjectID int64  `bun:"object_id"`
	ParentID int64  `bun:"parent_id"`
	URL      string `bun:"item_url"`
}

func (r *NavMenuRepo) Menus(ctx context.Context) ([]domain.NavMenu, error) {
	var rows []navMenuRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", "nav_menu").
		OrderExpr("t.term_id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]domain.NavMenu, len(rows))
	for i, row := range rows {
		menu, err := r.menuByTermID(ctx, row.ID)
		if err != nil {
			return nil, err
		}
		out[i] = menu
	}
	return out, nil
}

func (r *NavMenuRepo) MenuBySlug(ctx context.Context, slug string) (domain.NavMenu, error) {
	var row navMenuRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", "nav_menu").
		Where("t.slug = ?", slug).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NavMenu{}, nil
	}
	if err != nil {
		return domain.NavMenu{}, err
	}
	return r.menuByTermID(ctx, row.ID)
}

func (r *NavMenuRepo) MenuByID(ctx context.Context, id int64) (domain.NavMenu, error) {
	menu, err := r.menuByTermID(ctx, id)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NavMenu{}, nil
	}
	return menu, err
}

func (r *NavMenuRepo) MenuByLocation(ctx context.Context, theme, location string) (domain.NavMenu, error) {
	optionName := "theme_mods_" + theme
	var raw string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"options")).
		Column("option_value").
		Where("option_name = ?", optionName).
		Limit(1).
		Scan(ctx, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NavMenu{}, nil
	}
	if err != nil {
		return domain.NavMenu{}, err
	}
	decoded, err := php.Unserialize(raw)
	if err != nil {
		return domain.NavMenu{}, nil
	}
	mods, ok := decoded.(map[string]any)
	if !ok {
		return domain.NavMenu{}, nil
	}
	navAny, ok := mods["nav_menu_locations"]
	if !ok {
		return domain.NavMenu{}, nil
	}
	navMap, ok := navAny.(map[string]any)
	if !ok {
		return domain.NavMenu{}, nil
	}
	assigned, ok := navMap[location]
	if !ok {
		return domain.NavMenu{}, nil
	}
	termID, ok := asInt64(assigned)
	if !ok || termID == 0 {
		return domain.NavMenu{}, nil
	}
	menu, err := r.menuByTermID(ctx, termID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.NavMenu{}, nil
	}
	return menu, err
}

func (r *NavMenuRepo) menuByTermID(ctx context.Context, termID int64) (domain.NavMenu, error) {
	var row navMenuRow
	err := r.db.NewSelect().
		TableExpr("? AS t", bun.Ident(r.prefix+"terms")).
		ColumnExpr("t.term_id, t.name, t.slug").
		Join("JOIN ? AS tt ON tt.term_id = t.term_id", bun.Ident(r.prefix+"term_taxonomy")).
		Where("tt.taxonomy = ?", "nav_menu").
		Where("t.term_id = ?", termID).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NavMenu{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.NavMenu{}, err
	}
	items, err := r.menuItems(ctx, termID)
	if err != nil {
		return domain.NavMenu{}, err
	}
	return domain.NavMenu{ID: row.ID, Name: row.Name, Slug: row.Slug, Items: items}, nil
}

func (r *NavMenuRepo) menuItems(ctx context.Context, termID int64) ([]domain.NavMenuItem, error) {
	q := `SELECT p.ID, p.post_title, p.menu_order,
COALESCE(MAX(CASE WHEN pm.meta_key = '_menu_item_type' THEN pm.meta_value END), '') AS item_type,
COALESCE(MAX(CASE WHEN pm.meta_key = '_menu_item_object' THEN pm.meta_value END), '') AS item_object,
COALESCE(MAX(CASE WHEN pm.meta_key = '_menu_item_object_id' THEN pm.meta_value END), '0') AS object_id,
COALESCE(MAX(CASE WHEN pm.meta_key = '_menu_item_menu_item_parent' THEN pm.meta_value END), '0') AS parent_id,
COALESCE(MAX(CASE WHEN pm.meta_key = '_menu_item_url' THEN pm.meta_value END), '') AS item_url
FROM ` + r.prefix + `posts p
JOIN ` + r.prefix + `term_relationships tr ON tr.object_id = p.ID
JOIN ` + r.prefix + `term_taxonomy tt ON tt.term_taxonomy_id = tr.term_taxonomy_id
LEFT JOIN ` + r.prefix + `postmeta pm ON pm.post_id = p.ID
WHERE tt.taxonomy = ? AND tt.term_id = ? AND p.post_type = ?
GROUP BY p.ID, p.post_title, p.menu_order
ORDER BY p.menu_order ASC, p.ID ASC`
	rows, err := r.db.QueryContext(ctx, rebind.Rebind(vendorOf(r.db), q), "nav_menu", termID, "nav_menu_item")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var flat []domain.NavMenuItem
	for rows.Next() {
		var row navItemRow
		var objectID, parentID string
		if err := rows.Scan(&row.ID, &row.Title, &row.Order, &row.Type, &row.Object, &objectID, &parentID, &row.URL); err != nil {
			return nil, err
		}
		row.ObjectID, _ = strconv.ParseInt(objectID, 10, 64)
		row.ParentID, _ = strconv.ParseInt(parentID, 10, 64)
		label, url, err := r.resolveMenuTarget(ctx, row)
		if err != nil {
			return nil, err
		}
		flat = append(flat, domain.NavMenuItem{ID: row.ID, Label: label, URL: url, Type: row.Type, Object: row.Object, ObjectID: row.ObjectID, ParentID: row.ParentID, Order: row.Order})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return buildMenuTree(flat), nil
}

func (r *NavMenuRepo) resolveMenuTarget(ctx context.Context, row navItemRow) (string, string, error) {
	if row.Type == "custom" {
		return row.Title, row.URL, nil
	}
	if row.Type == "post_type" {
		var title, slug string
		err := r.db.NewSelect().
			TableExpr("?", bun.Ident(r.prefix+"posts")).
			Column("post_title", "post_name").
			Where("? = ?", bun.Ident("ID"), row.ObjectID).
			Limit(1).
			Scan(ctx, &title, &slug)
		if errors.Is(err, sql.ErrNoRows) {
			return fallbackLabel(row.Title), row.URL, nil
		}
		if err != nil {
			return "", "", err
		}
		label := row.Title
		if strings.TrimSpace(label) == "" {
			label = title
		}
		return label, "/" + slug, nil
	}
	if row.Type == "taxonomy" {
		var name, slug string
		err := r.db.NewSelect().
			TableExpr("?", bun.Ident(r.prefix+"terms")).
			Column("name", "slug").
			Where("term_id = ?", row.ObjectID).
			Limit(1).
			Scan(ctx, &name, &slug)
		if errors.Is(err, sql.ErrNoRows) {
			return fallbackLabel(row.Title), row.URL, nil
		}
		if err != nil {
			return "", "", err
		}
		label := row.Title
		if strings.TrimSpace(label) == "" {
			label = name
		}
		return label, fmt.Sprintf("/%s/%s", row.Object, slug), nil
	}
	return fallbackLabel(row.Title), row.URL, nil
}

func buildMenuTree(flat []domain.NavMenuItem) []domain.NavMenuItem {
	children := map[int64][]domain.NavMenuItem{}
	for _, item := range flat {
		children[item.ParentID] = append(children[item.ParentID], item)
	}
	// visited guards against corrupt data (a self-parenting or cyclic
	// _menu_item_menu_item_parent chain) causing unbounded recursion: an id
	// already on the current descent path is cut off rather than re-entered.
	// It is un-marked on the way back up so unrelated sibling subtrees are
	// never falsely blocked.
	visited := map[int64]bool{}
	var attach func(parent int64) []domain.NavMenuItem
	attach = func(parent int64) []domain.NavMenuItem {
		items := children[parent]
		sort.SliceStable(items, func(i, j int) bool {
			if items[i].Order == items[j].Order {
				return items[i].ID < items[j].ID
			}
			return items[i].Order < items[j].Order
		})
		for i := range items {
			id := items[i].ID
			if visited[id] {
				items[i].Children = nil
				continue
			}
			visited[id] = true
			items[i].Children = attach(id)
			delete(visited, id)
		}
		return items
	}
	return attach(0)
}

func fallbackLabel(s string) string {
	if strings.TrimSpace(s) == "" {
		return "Untitled"
	}
	return s
}

func asInt64(v any) (int64, bool) {
	switch t := v.(type) {
	case int:
		return int64(t), true
	case int64:
		return t, true
	case string:
		n, err := strconv.ParseInt(t, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
