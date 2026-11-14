package content

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// CustomPostService tests (M12 task 7.3, TDD RED). Shape under test, decided
// from design Component 6:
//
//	func NewCustomPostService(nodes domain.PostNodeReader, posts domain.PostRepository) *CustomPostService
//	func (s *CustomPostService) Resolve(ctx, typeName string, hierarchical bool, segments []string) (domain.Post, []string, error)
//	func (s *CustomPostService) AncestryFor(ctx, typeName string, id int64) ([]string, error)
//	func (s *CustomPostService) Archive(ctx, typeName string, page, perPage int) (Archive, error)
//
// Archive.Heading is left UNSET by the service: it holds no type registry, only
// a typeName, so the handler (which owns the registered PostType) sets
// pt.Heading().

// fakeNodeReader serves a fixed node graph per post type, honouring the
// ID>afterID keyset cursor and limit, and records the type of every call.
type fakeNodeReader struct {
	byType map[string][]domain.PostNode
	calls  int
	types  []string
	err    error
}

func newNodeReader(postType string, nodes ...domain.PostNode) *fakeNodeReader {
	cp := slices.Clone(nodes)
	sort.Slice(cp, func(i, j int) bool { return cp[i].ID < cp[j].ID })
	return &fakeNodeReader{byType: map[string][]domain.PostNode{postType: cp}}
}

func (r *fakeNodeReader) ListPostNodes(ctx context.Context, postType string, afterID int64, limit int) ([]domain.PostNode, error) {
	r.calls++
	r.types = append(r.types, postType)
	if r.err != nil {
		return nil, r.err
	}
	var out []domain.PostNode
	for _, n := range r.byType[postType] {
		if n.ID <= afterID {
			continue
		}
		out = append(out, n)
		if limit > 0 && len(out) == limit {
			break
		}
	}
	return out, nil
}

// cptRepo is a fakePostRepo whose BySlug/PublishedByID answer per (type, key)
// so type scoping and per-ID leaf confirmation are observable. Only published
// posts are registered; anything else is ErrNotFound, like the real filters.
type cptRepo struct {
	*fakePostRepo
	slugPosts map[string]domain.Post // "type/slug"
	idPosts   map[string]domain.Post // "type/id"
	bySlugN   int
	byIDN     int
}

func newCPTRepo() *cptRepo {
	return &cptRepo{
		fakePostRepo: &fakePostRepo{},
		slugPosts:    map[string]domain.Post{},
		idPosts:      map[string]domain.Post{},
	}
}

func (r *cptRepo) add(typeName string, p domain.Post) {
	r.slugPosts[typeName+"/"+p.Slug] = p
	r.idPosts[fmt.Sprintf("%s/%d", typeName, p.ID)] = p
}

func (r *cptRepo) BySlug(ctx context.Context, slug string, types ...string) (domain.Post, error) {
	r.bySlugN++
	r.bySlugSlug, r.bySlugTypes = slug, types
	if len(types) == 1 {
		if p, ok := r.slugPosts[types[0]+"/"+slug]; ok {
			return p, nil
		}
	}
	return domain.Post{}, domain.ErrNotFound
}

func (r *cptRepo) PublishedByID(ctx context.Context, id int64, types ...string) (domain.Post, error) {
	r.byIDN++
	r.byIDID, r.byIDTypes = id, types
	if len(types) == 1 {
		if p, ok := r.idPosts[fmt.Sprintf("%s/%d", types[0], id)]; ok {
			return p, nil
		}
	}
	return domain.Post{}, domain.ErrNotFound
}

func cptPost(id int64, slug string) domain.Post {
	return domain.Post{ID: id, Slug: slug, Title: "T-" + slug, Status: "publish"}
}

func TestNewCustomPostServicePanicsOnNil(t *testing.T) {
	nodes, repo := newNodeReader("volume"), newCPTRepo()
	for name, f := range map[string]func(){
		"nil nodes": func() { NewCustomPostService(nil, repo) },
		"nil posts": func() { NewCustomPostService(nodes, nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: expected panic", name)
				}
			}()
			f()
		}()
	}
}

func TestCustomPostResolveFlatScopedToType(t *testing.T) {
	nodes := newNodeReader("book")
	repo := newCPTRepo()
	repo.add("book", cptPost(10, "dune"))
	repo.add("post", cptPost(11, "hello")) // a post of another type
	svc := NewCustomPostService(nodes, repo)

	got, anc, err := svc.Resolve(context.Background(), "book", false, []string{"dune"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != 10 || !slices.Equal(anc, []string{"dune"}) {
		t.Fatalf("got id=%d ancestry=%v", got.ID, anc)
	}
	if !slices.Equal(repo.bySlugTypes, []string{"book"}) || repo.bySlugSlug != "dune" {
		t.Fatalf("BySlug(%q, %v), want (dune, [book])", repo.bySlugSlug, repo.bySlugTypes)
	}
	if nodes.calls != 0 {
		t.Fatalf("non-hierarchical Resolve read nodes %d times, want 0", nodes.calls)
	}

	// A post of the same slug does not resolve for book, and the reverse.
	if _, _, err := svc.Resolve(context.Background(), "book", false, []string{"hello"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("book/hello err=%v, want ErrNotFound", err)
	}
	if _, _, err := svc.Resolve(context.Background(), "post", false, []string{"dune"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("post/dune err=%v, want ErrNotFound", err)
	}
}

func TestCustomPostResolveFlatRejectsWrongSegmentCount(t *testing.T) {
	repo := newCPTRepo()
	repo.add("book", cptPost(10, "dune"))
	svc := NewCustomPostService(newNodeReader("book"), repo)
	for _, segs := range [][]string{nil, {}, {"a", "dune"}, {"dune", "x", "y"}} {
		if _, _, err := svc.Resolve(context.Background(), "book", false, segs); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("segments %v err=%v, want ErrNotFound", segs, err)
		}
	}
	if repo.bySlugN != 0 {
		t.Fatalf("BySlug called %d times for rejected segment counts", repo.bySlugN)
	}
}

// volumeGraph: part-one(2) under vol-1(1); vol-2(3) with its own part-one(4).
func volumeGraph() *fakeNodeReader {
	return newNodeReader("volume",
		pageNode(1, 0, "vol-1", "publish"),
		pageNode(2, 1, "part-one", "publish"),
		pageNode(3, 0, "vol-2", "publish"),
		pageNode(4, 3, "part-one", "publish"),
		pageNode(5, 0, "draft-vol", "draft"),
		pageNode(6, 1, "draft-part", "draft"),
	)
}

func volumeRepo() *cptRepo {
	repo := newCPTRepo()
	repo.add("volume", cptPost(1, "vol-1"))
	repo.add("volume", cptPost(2, "part-one"))
	repo.add("volume", cptPost(3, "vol-2"))
	repo.add("volume", cptPost(4, "part-one"))
	return repo
}

func TestCustomPostResolveHierarchicalNestedLeaf(t *testing.T) {
	nodes := volumeGraph()
	svc := NewCustomPostService(nodes, volumeRepo())
	got, anc, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-1", "part-one"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != 2 || !slices.Equal(anc, []string{"vol-1", "part-one"}) {
		t.Fatalf("got id=%d ancestry=%v", got.ID, anc)
	}
	if nodes.calls == 0 {
		t.Fatal("hierarchical Resolve never read the node graph")
	}
	for _, ty := range nodes.types {
		if ty != "volume" {
			t.Fatalf("ListPostNodes called with type %q, want volume", ty)
		}
	}
}

// Finding 5: two leaves sharing a slug under different parents each serve
// their own body; the leaf is confirmed by ID, never by BySlug.
func TestCustomPostResolveConfirmsLeafByIDNotSlug(t *testing.T) {
	repo := volumeRepo()
	svc := NewCustomPostService(volumeGraph(), repo)

	a, _, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-1", "part-one"})
	if err != nil {
		t.Fatalf("vol-1/part-one: %v", err)
	}
	b, ancB, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-2", "part-one"})
	if err != nil {
		t.Fatalf("vol-2/part-one: %v", err)
	}
	if a.ID != 2 || b.ID != 4 {
		t.Fatalf("ids = %d, %d; want 2, 4", a.ID, b.ID)
	}
	if !slices.Equal(ancB, []string{"vol-2", "part-one"}) {
		t.Fatalf("ancestry %v", ancB)
	}
	if repo.bySlugN != 0 {
		t.Fatalf("BySlug called %d times; leaf must be confirmed by PublishedByID", repo.bySlugN)
	}
	if repo.byIDID != 4 || !slices.Equal(repo.byIDTypes, []string{"volume"}) {
		t.Fatalf("PublishedByID(%d, %v), want (4, [volume])", repo.byIDID, repo.byIDTypes)
	}
}

func TestCustomPostResolveNonPublishedLeafIsNotFound(t *testing.T) {
	// draft-part is in the graph (all statuses) but PublishedByID rejects it.
	svc := NewCustomPostService(volumeGraph(), volumeRepo())
	_, _, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-1", "draft-part"})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err=%v, want ErrNotFound", err)
	}
}

func TestCustomPostResolveWrongAncestryRecoversToPublishedNode(t *testing.T) {
	svc := NewCustomPostService(volumeGraph(), volumeRepo())
	_, _, err := svc.Resolve(context.Background(), "volume", true, []string{"wrong", "part-one"})
	var moved *PageMovedError
	if !errors.As(err, &moved) {
		t.Fatalf("err=%v, want *PageMovedError", err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Fatal("PageMovedError must not wrap ErrNotFound")
	}
	// Lowest-ID node carrying the slug wins: part-one(2) under vol-1.
	if !slices.Equal(moved.Ancestry, []string{"vol-1", "part-one"}) {
		t.Fatalf("ancestry %v", moved.Ancestry)
	}

	// A flat (root-level) request for a nested published node also recovers.
	_, _, err = svc.Resolve(context.Background(), "volume", true, []string{"vol-2"})
	if err != nil {
		t.Fatalf("root node resolves directly: %v", err)
	}
}

// Finding 6: M15 discloses that a draft exists by redirecting; this must not.
func TestCustomPostResolveRecoveryToNonPublishedNodeIsNotFound(t *testing.T) {
	svc := NewCustomPostService(volumeGraph(), volumeRepo())
	for _, segs := range [][]string{{"anything", "draft-part"}, {"draft-vol"}, {"x", "draft-vol"}} {
		_, _, err := svc.Resolve(context.Background(), "volume", true, segs)
		var moved *PageMovedError
		if errors.As(err, &moved) {
			t.Errorf("%v: disclosed draft via PageMovedError %v", segs, moved.Ancestry)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("%v: err=%v, want ErrNotFound", segs, err)
		}
	}
}

func TestCustomPostResolveOrphanParentDegradesToShorterPath(t *testing.T) {
	nodes := newNodeReader("volume",
		pageNode(2, 99, "team", "publish"), // parent 99 never imported
		pageNode(3, 2, "history", "publish"),
	)
	repo := newCPTRepo()
	repo.add("volume", cptPost(3, "history"))
	svc := NewCustomPostService(nodes, repo)
	got, anc, err := svc.Resolve(context.Background(), "volume", true, []string{"team", "history"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.ID != 3 || !slices.Equal(anc, []string{"team", "history"}) {
		t.Fatalf("id=%d ancestry=%v", got.ID, anc)
	}
}

func TestCustomPostResolveParentCycleTerminates(t *testing.T) {
	nodes := newNodeReader("volume",
		pageNode(1, 2, "a", "publish"),
		pageNode(2, 1, "b", "publish"),
	)
	repo := newCPTRepo()
	repo.add("volume", cptPost(1, "a"))
	repo.add("volume", cptPost(2, "b"))
	svc := NewCustomPostService(nodes, repo)

	done := make(chan struct{})
	var errReach, errEqual error
	go func() {
		defer close(done)
		_, _, errReach = svc.Resolve(context.Background(), "volume", true, []string{"a"})
		// Canonical ancestry of node 1 is [b a]; requesting exactly that path
		// is the equal-ancestry corruption guard: ErrNotFound, never a
		// redirect to itself.
		_, _, errEqual = svc.Resolve(context.Background(), "volume", true, []string{"b", "a"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Resolve hung on a parent cycle")
	}
	if errReach == nil {
		t.Fatal("cycle member resolved with 200 from a path no root reaches")
	}
	if !errors.Is(errEqual, domain.ErrNotFound) {
		t.Fatalf("equal-ancestry cycle recovery err=%v, want ErrNotFound", errEqual)
	}
}

// Req 5.6: the graph is loaded once per Resolve. 600 nodes span two keyset
// pages (page size 500), so one load is exactly 2 calls, not 2 per segment.
func TestCustomPostResolveLoadsGraphOnce(t *testing.T) {
	var ns []domain.PostNode
	ns = append(ns, pageNode(1, 0, "root", "publish"), pageNode(2, 1, "mid", "publish"), pageNode(3, 2, "leaf", "publish"))
	for i := int64(4); i <= 600; i++ {
		ns = append(ns, pageNode(i, 0, fmt.Sprintf("n%d", i), "publish"))
	}
	nodes := newNodeReader("volume", ns...)
	repo := newCPTRepo()
	repo.add("volume", cptPost(3, "leaf"))
	svc := NewCustomPostService(nodes, repo)

	if _, _, err := svc.Resolve(context.Background(), "volume", true, []string{"root", "mid", "leaf"}); err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if nodes.calls != 2 {
		t.Fatalf("ListPostNodes calls = %d, want 2 (one paginated load)", nodes.calls)
	}

	nodes.calls = 0
	if _, _, err := svc.Resolve(context.Background(), "volume", true, []string{"nope", "leaf"}); err == nil {
		t.Fatal("expected a recovery error")
	}
	if nodes.calls != 2 {
		t.Fatalf("recovery path made %d calls, want 2", nodes.calls)
	}
}

func TestCustomPostAncestryForMatchesResolve(t *testing.T) {
	nodes := volumeGraph()
	svc := NewCustomPostService(nodes, volumeRepo())
	_, want, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-2", "part-one"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	nodes.types = nil
	got, err := svc.AncestryFor(context.Background(), "volume", 4)
	if err != nil {
		t.Fatalf("AncestryFor: %v", err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("AncestryFor=%v, Resolve=%v", got, want)
	}
	if len(nodes.types) == 0 || nodes.types[0] != "volume" {
		t.Fatalf("node read types %v, want volume", nodes.types)
	}
	if none, err := svc.AncestryFor(context.Background(), "volume", 999); err != nil || none != nil {
		t.Fatalf("unknown id: %v, %v; want nil, nil", none, err)
	}
}

func TestCustomPostAncestryForPropagatesReadError(t *testing.T) {
	boom := errors.New("boom")
	nodes := volumeGraph()
	nodes.err = boom
	svc := NewCustomPostService(nodes, volumeRepo())
	if _, err := svc.AncestryFor(context.Background(), "volume", 1); !errors.Is(err, boom) {
		t.Fatalf("err=%v, want boom", err)
	}
	if _, _, err := svc.Resolve(context.Background(), "volume", true, []string{"vol-1"}); !errors.Is(err, boom) {
		t.Fatalf("Resolve err=%v, want boom", err)
	}
}

func TestCustomPostArchiveFiltersToTypeAndPaginates(t *testing.T) {
	repo := newCPTRepo()
	repo.archivePosts = []domain.Post{cptPost(3, "c"), cptPost(2, "b")}
	repo.archiveCount = 7
	svc := NewCustomPostService(newNodeReader("book"), repo)

	got, err := svc.Archive(context.Background(), "book", 2, 2)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	for name, f := range map[string]domain.ArchiveFilter{"list": repo.archiveFilter, "count": repo.archiveCountFilter} {
		if !slices.Equal(f.Types, []string{"book"}) {
			t.Errorf("%s filter Types=%v, want [book]", name, f.Types)
		}
		if f.Taxonomy != "" || f.AuthorID != 0 || len(f.TermIDs) != 0 {
			t.Errorf("%s filter has extra constraints: %+v", name, f)
		}
	}
	if repo.archiveLimit != 2 || repo.archiveOffset != 2 {
		t.Fatalf("limit=%d offset=%d, want 2, 2", repo.archiveLimit, repo.archiveOffset)
	}
	// Order is delegated to the repo: the service must not re-sort.
	if len(got.Posts) != 2 || got.Posts[0].ID != 3 || got.Posts[1].ID != 2 {
		t.Fatalf("posts=%v, want repo order [3 2]", got.Posts)
	}
	if got.Page != newPage(2, 2, 7) {
		t.Fatalf("page=%+v, want %+v", got.Page, newPage(2, 2, 7))
	}
	if got.Heading != "" || got.Ancestry != nil {
		t.Fatalf("Heading=%q Ancestry=%v, want both unset", got.Heading, got.Ancestry)
	}
}

func TestCustomPostArchiveClampsPaging(t *testing.T) {
	repo := newCPTRepo()
	svc := NewCustomPostService(newNodeReader("book"), repo)
	if _, err := svc.Archive(context.Background(), "book", 0, 0); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	wantLimit, wantOffset, _ := clamp(0, 0)
	if repo.archiveLimit != wantLimit || repo.archiveOffset != wantOffset {
		t.Fatalf("limit=%d offset=%d, want %d, %d", repo.archiveLimit, repo.archiveOffset, wantLimit, wantOffset)
	}
}

func TestCustomPostArchiveEmptyIsNotNotFound(t *testing.T) {
	svc := NewCustomPostService(newNodeReader("book"), newCPTRepo())
	got, err := svc.Archive(context.Background(), "book", 1, 10)
	if err != nil {
		t.Fatalf("empty archive err=%v, want nil", err)
	}
	if got.Page.Total != 0 || len(got.Posts) != 0 {
		t.Fatalf("got %+v, want empty archive with Total 0", got)
	}
}

func TestCustomPostArchivePropagatesErrors(t *testing.T) {
	boom := errors.New("boom")
	repo := newCPTRepo()
	repo.archiveErr = boom
	svc := NewCustomPostService(newNodeReader("book"), repo)
	if _, err := svc.Archive(context.Background(), "book", 1, 10); !errors.Is(err, boom) {
		t.Fatalf("list err=%v, want boom", err)
	}
	repo2 := newCPTRepo()
	repo2.archiveCountErr = boom
	svc = NewCustomPostService(newNodeReader("book"), repo2)
	if _, err := svc.Archive(context.Background(), "book", 1, 10); !errors.Is(err, boom) {
		t.Fatalf("count err=%v, want boom", err)
	}
}
