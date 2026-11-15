package content

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// pageRepo is a per-ID, per-type fake PostRepository for PageService.Resolve.
// Only published rows are registered; anything else is ErrNotFound, like the
// real filters. BySlug answers from slugRows regardless of the walked path, so
// a slug-keyed confirmation is observable: tests point it at a deliberately
// wrong same-slug row (the other page, or a post).
type pageRepo struct {
	*fakePostRepo
	idRows   map[string]domain.Post // "type/id"
	slugRows map[string]domain.Post // slug -> row BySlug returns (wrong on purpose)
	bySlugN  int
	byIDN    int
	byIDCall []string // "type/id" for each PublishedByID call
}

func newPageRepo() *pageRepo {
	return &pageRepo{
		fakePostRepo: &fakePostRepo{},
		idRows:       map[string]domain.Post{},
		slugRows:     map[string]domain.Post{},
	}
}

func (r *pageRepo) addPublished(typeName string, p domain.Post) {
	p.Type = typeName
	r.idRows[fmt.Sprintf("%s/%d", typeName, p.ID)] = p
}

func (r *pageRepo) BySlug(ctx context.Context, slug string, types ...string) (domain.Post, error) {
	r.bySlugN++
	if p, ok := r.slugRows[slug]; ok {
		return p, nil
	}
	return domain.Post{}, domain.ErrNotFound
}

func (r *pageRepo) PublishedByID(ctx context.Context, id int64, types ...string) (domain.Post, error) {
	r.byIDN++
	if len(types) == 0 {
		types = []string{"post", "page"}
	}
	for _, ty := range types {
		r.byIDCall = append(r.byIDCall, fmt.Sprintf("%s/%d", ty, id))
		if p, ok := r.idRows[fmt.Sprintf("%s/%d", ty, id)]; ok {
			return p, nil
		}
	}
	return domain.Post{}, domain.ErrNotFound
}

func pubPage(id int64, slug string) domain.Post {
	return domain.Post{ID: id, Slug: slug, Title: "T-" + slug, Status: "publish"}
}

// Defect A: same slug under different parents; each URL serves its own page.
func TestPageServiceResolveSameSlugLeaves(t *testing.T) {
	pages := newPageReader(
		pageNode(1, 0, "a", "publish"), pageNode(11, 1, "x", "publish"),
		pageNode(2, 0, "b", "publish"), pageNode(12, 2, "x", "publish"),
	)
	repo := newPageRepo()
	repo.addPublished("page", pubPage(11, "x"))
	repo.addPublished("page", pubPage(12, "x"))
	// BySlug picks one row for both URLs, as the real repo does.
	repo.slugRows["x"] = repo.idRows["page/11"]
	svc := NewPageService(pages, repo)

	for _, tc := range []struct {
		segs []string
		id   int64
	}{{[]string{"a", "x"}, 11}, {[]string{"b", "x"}, 12}} {
		got, anc, err := svc.Resolve(context.Background(), tc.segs)
		if err != nil {
			t.Fatalf("Resolve(%v): %v", tc.segs, err)
		}
		if got.ID != tc.id || !slices.Equal(anc, tc.segs) {
			t.Errorf("Resolve(%v) = id %d ancestry %v, want id %d ancestry %v", tc.segs, got.ID, anc, tc.id, tc.segs)
		}
	}
}

// Defect B: a same-slug post must not shadow the page.
func TestPageServiceResolvePostDoesNotShadowPage(t *testing.T) {
	pages := newPageReader(pageNode(1, 0, "about", "publish"), pageNode(5, 1, "team", "publish"))
	repo := newPageRepo()
	repo.addPublished("page", pubPage(5, "team"))
	post := pubPage(9, "team")
	post.Type = "post"
	repo.slugRows["team"] = post
	svc := NewPageService(pages, repo)

	got, anc, err := svc.Resolve(context.Background(), []string{"about", "team"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != 5 || got.Type != "page" || !slices.Equal(anc, []string{"about", "team"}) {
		t.Errorf("got id=%d type=%q ancestry=%v, want page 5", got.ID, got.Type, anc)
	}
	if repo.bySlugN != 0 {
		t.Errorf("BySlug called %d times, want 0", repo.bySlugN)
	}
	if !slices.Contains(repo.byIDCall, "page/5") || slices.Contains(repo.byIDCall, "post/5") {
		t.Errorf("PublishedByID calls = %v, want (5, \"page\") only", repo.byIDCall)
	}
}

// Defect C: an unpublished page must 404, never 301.
func TestPageServiceResolveDraftRedirectIsNotFound(t *testing.T) {
	pages := newPageReader(pageNode(3, 0, "handbook", "publish"), pageNode(7, 3, "secret", "draft"))
	svc := NewPageService(pages, newPageRepo()) // draft is not registered as published

	for _, segs := range [][]string{{"secret"}, {"wrong", "secret"}} {
		_, _, err := svc.Resolve(context.Background(), segs)
		var moved *PageMovedError
		if errors.As(err, &moved) {
			t.Errorf("Resolve(%v) returned *PageMovedError for a draft (page %d)", segs, moved.Page.ID)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("Resolve(%v) err = %v, want ErrNotFound", segs, err)
		}
	}
}

// Guard (passes before and after): a published flat URL still moves.
func TestPageServiceResolvePublishedFlatURLMoves(t *testing.T) {
	pages := newPageReader(pageNode(3, 0, "handbook", "publish"), pageNode(8, 3, "onboarding", "publish"))
	repo := newPageRepo()
	repo.addPublished("page", pubPage(8, "onboarding"))
	svc := NewPageService(pages, repo)

	_, _, err := svc.Resolve(context.Background(), []string{"onboarding"})
	var moved *PageMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("err = %v, want *PageMovedError", err)
	}
	if !slices.Equal(moved.Ancestry, []string{"handbook", "onboarding"}) {
		t.Errorf("Ancestry = %v, want [handbook onboarding]", moved.Ancestry)
	}
}

// ---- Task 2: preservation (non-buggy inputs; unique slugs, no post competitor,
// published or genuinely absent pages). The repo answers BySlug and
// PublishedByID consistently, so the baseline and the fixed code agree.

// genGraph builds a random forest of unique-slug pages (parent ID < child ID,
// so no cycles or orphans) and a consistent repo over its published nodes.
func genGraph(rng *rand.Rand) (nodes []domain.PageNode, repo *pageRepo) {
	repo = newPageRepo()
	n := 2 + rng.Intn(9)
	for i := 1; i <= n; i++ {
		var parent int64
		if i > 1 && rng.Intn(4) != 0 {
			parent = int64(1 + rng.Intn(i-1))
		}
		status := "publish"
		if rng.Intn(3) == 0 {
			status = "draft"
		}
		slug := fmt.Sprintf("s%d", i)
		nodes = append(nodes, pageNode(int64(i), parent, slug, status))
		if status == "publish" {
			p := pubPage(int64(i), slug)
			repo.addPublished("page", p)
			repo.slugRows[slug] = repo.idRows[fmt.Sprintf("page/%d", i)]
		}
	}
	return nodes, repo
}

func chainOf(nodes []domain.PageNode, id int64) []string {
	byID := map[int64]domain.PageNode{}
	for _, n := range nodes {
		byID[n.ID] = n
	}
	var out []string
	for cur := byID[id]; ; cur = byID[cur.ParentID] {
		out = append([]string{cur.Slug}, out...)
		if cur.ParentID == 0 {
			return out
		}
	}
}

func TestPageServiceResolvePreservationProperty(t *testing.T) {
	rng := rand.New(rand.NewSource(15))
	ctx := context.Background()
	for iter := 0; iter < 200; iter++ {
		nodes, repo := genGraph(rng)
		svc := NewPageService(newPageReader(nodes...), repo)
		byID := map[int64]domain.PageNode{}
		for _, n := range nodes {
			byID[n.ID] = n
		}
		for _, n := range nodes {
			published := n.Status == "publish"
			chain := chainOf(nodes, n.ID)

			// Walk-found leaf: published returns page+ancestry, draft is ErrNotFound.
			got, anc, err := svc.Resolve(ctx, chain)
			if published {
				if err != nil || got.ID != n.ID || !slices.Equal(anc, chain) {
					t.Fatalf("iter %d: Resolve(%v) = id %d anc %v err %v, want id %d anc %v", iter, chain, got.ID, anc, err, n.ID, chain)
				}
			} else if !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("iter %d: draft leaf Resolve(%v) err = %v, want ErrNotFound", iter, chain, err)
			}

			// Published flat / wrong-ancestor paths: *PageMovedError, canonical ancestry.
			if !published || n.ParentID == 0 {
				continue
			}
			wrong := nodes[rng.Intn(len(nodes))]
			paths := [][]string{{n.Slug}}
			if wrong.ID != n.ParentID && wrong.ID != n.ID {
				paths = append(paths, []string{wrong.Slug, n.Slug})
			}
			for _, segs := range paths {
				_, _, err := svc.Resolve(ctx, segs)
				var moved *PageMovedError
				if !errors.As(err, &moved) || moved.Page.ID != n.ID || !slices.Equal(moved.Ancestry, chain) {
					t.Fatalf("iter %d: Resolve(%v) err = %v, want moved to %v", iter, segs, err, chain)
				}
			}
		}
		// Unknown final segment (top level and nested) and empty segments.
		for _, segs := range [][]string{nil, {}, {"nope"}, {nodes[0].Slug, "nope"}} {
			if _, _, err := svc.Resolve(ctx, segs); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("iter %d: Resolve(%v) err = %v, want ErrNotFound", iter, segs, err)
			}
		}
	}
}

// errPageRepo fails every confirmation read with a non-NotFound error.
type errPageRepo struct {
	*pageRepo
	err error
}

func (r *errPageRepo) BySlug(context.Context, string, ...string) (domain.Post, error) {
	return domain.Post{}, r.err
}

func (r *errPageRepo) PublishedByID(context.Context, int64, ...string) (domain.Post, error) {
	return domain.Post{}, r.err
}

func TestPageServiceResolvePropagatesRepositoryError(t *testing.T) {
	boom := errors.New("boom")
	pages := newPageReader(pageNode(1, 0, "about", "publish"), pageNode(2, 1, "team", "publish"))
	svc := NewPageService(pages, &errPageRepo{pageRepo: newPageRepo(), err: boom})
	_, _, err := svc.Resolve(context.Background(), []string{"about", "team"})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the repository error", err)
	}
}
