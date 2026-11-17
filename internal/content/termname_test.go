package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// --- M18: term names for every taxonomy (task 5.1, RED) ----------------------
//
// Written failing-first. Today only registered taxonomies reduce a term name,
// and Update never reads the stored row. Until task 5.2 lands:
//   - Create of `<script>x</script>` for category/post_tag is written verbatim
//     instead of failing with ErrTermNameEmpty;
//   - the update path performs no stored read (tnmWriter.reads stays 0).
//
// The existing fakeTermWriter is reused unchanged; tnmWriter wraps it with a
// read counter and an injectable read error.

const tnmStoredName = "News &amp; Events"

// tnmWriter counts TermsByIDs reads and can fail them.
type tnmWriter struct {
	*fakeTermWriter
	reads   int
	readErr error
}

func (w *tnmWriter) TermsByIDs(ctx context.Context, ids []int64) ([]domain.Term, error) {
	w.reads++
	if w.readErr != nil {
		return nil, w.readErr
	}
	return w.fakeTermWriter.TermsByIDs(ctx, ids)
}

func tnmNewWriter() *tnmWriter {
	cats := []domain.Term{
		{ID: 30, Name: "C1", Slug: "c1", Taxonomy: TaxonomyCategory},
		{ID: 40, Name: tnmStoredName, Slug: "news", Taxonomy: TaxonomyCategory},
	}
	tags := []domain.Term{
		{ID: 41, Name: tnmStoredName, Slug: "news", Taxonomy: taxonomyPostTag},
	}
	genre := []domain.Term{
		{ID: 1, Name: "G1", Slug: "g1", Taxonomy: "genre"},
		{ID: 42, Name: tnmStoredName, Slug: "news", Taxonomy: "genre"},
	}
	f := &fakeTermWriter{
		byTaxonomy: map[string][]domain.Term{
			TaxonomyCategory: cats, taxonomyPostTag: tags, "genre": genre,
		},
		byIDs: map[int64]domain.Term{},
	}
	for _, ts := range f.byTaxonomy {
		for _, tm := range ts {
			f.byIDs[tm.ID] = tm
		}
	}
	return &tnmWriter{fakeTermWriter: f}
}

func tnmSvc(t *testing.T, w *tnmWriter) *TermWriteService {
	t.Helper()
	return NewTermWriteService(w,
		WithTermRegistry(customTermRegistry(t)),
		WithTermContentPolicy(sanitize.New()))
}

// tnmStoredID maps each taxonomy under test to its seeded stored term.
var tnmStoredID = map[string]int64{TaxonomyCategory: 40, taxonomyPostTag: 41, "genre": 42}

var tnmTaxonomies = []string{TaxonomyCategory, taxonomyPostTag, "genre"}

func tnmEditor() auth.Principal   { return actor(auth.RoleEditor, 1) }
func tnmAdmin() auth.Principal    { return actor(auth.RoleAdministrator, 2) }
func tnmNobody() auth.Principal   { return actor(auth.RoleSubscriber, 3) }
func tnmWrites(w *tnmWriter) bool { return w.created != nil || w.updated != nil }

func TestTermNameCreateMarkupOnlyIsEmpty(t *testing.T) { // 5.3
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			_, err := tnmSvc(t, w).Create(context.Background(), tnmEditor(),
				domain.Term{Name: "<script>x</script>", Slug: "x", Taxonomy: tax})
			if !errors.Is(err, ErrTermNameEmpty) {
				t.Fatalf("err = %v, want ErrTermNameEmpty", err)
			}
			if tnmWrites(w) {
				t.Errorf("writer called: %+v", w.created)
			}
		})
	}
}

func TestTermNameCreateReducesMarkup(t *testing.T) { // 5.1, 5.4
	actors := map[string]auth.Principal{"editor": tnmEditor(), "unfiltered_html admin": tnmAdmin()}
	for an, a := range actors {
		for _, tax := range tnmTaxonomies {
			t.Run(an+"/"+tax, func(t *testing.T) {
				w := tnmNewWriter()
				_, err := tnmSvc(t, w).Create(context.Background(), a,
					domain.Term{Name: "<b>Bold</b> & co", Slug: "bold", Taxonomy: tax})
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if w.created == nil || w.created.Name != "Bold & co" {
					t.Fatalf("created = %+v, want Name %q", w.created, "Bold & co")
				}
			})
		}
	}
}

func TestTermNameCreatePlainNameByteIdentical(t *testing.T) { // 15.1
	for _, tax := range []string{TaxonomyCategory, taxonomyPostTag} {
		for _, name := range []string{"Admin Cat", "Plain", "Two  Words"} {
			t.Run(tax+"/"+name, func(t *testing.T) {
				w := tnmNewWriter()
				_, err := tnmSvc(t, w).Create(context.Background(), tnmEditor(),
					domain.Term{Name: name, Slug: "plain-x", Taxonomy: tax})
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if w.created == nil || w.created.Name != name {
					t.Fatalf("created = %+v, want Name %q", w.created, name)
				}
			})
		}
	}
}

func TestTermNameUpdateChangedNameReduced(t *testing.T) { // 5.2
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			err := tnmSvc(t, w).Update(context.Background(), tnmEditor(), domain.Term{
				ID: tnmStoredID[tax], Name: "<b>Bold</b> & co", Slug: "news", Taxonomy: tax})
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if w.updated == nil || w.updated.Name != "Bold & co" {
				t.Fatalf("updated = %+v, want Name %q", w.updated, "Bold & co")
			}
		})
	}
}

func TestTermNameUpdateChangedMarkupOnlyIsEmpty(t *testing.T) { // 5.3
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			err := tnmSvc(t, w).Update(context.Background(), tnmEditor(), domain.Term{
				ID: tnmStoredID[tax], Name: "<script>x</script>", Slug: "news", Taxonomy: tax})
			if !errors.Is(err, ErrTermNameEmpty) {
				t.Fatalf("err = %v, want ErrTermNameEmpty", err)
			}
			if tnmWrites(w) {
				t.Errorf("writer called: %+v", w.updated)
			}
		})
	}
}

func TestTermNameUpdateUnchangedNameWrittenUnchanged(t *testing.T) { // 6.1
	cases := []struct {
		name   string
		slug   string
		parent int64
	}{
		{"name-only-identical", "news", 0},
		{"parent-only", "news", 30},
		{"slug-only", "news-2", 0},
	}
	for _, tax := range tnmTaxonomies {
		for _, c := range cases {
			if c.parent != 0 && tax == taxonomyPostTag {
				continue // flat taxonomy: a parent is invalid
			}
			if c.parent != 0 && tax == "genre" {
				c.parent = 1 // genre graph's root
			}
			t.Run(tax+"/"+c.name, func(t *testing.T) {
				w := tnmNewWriter()
				err := tnmSvc(t, w).Update(context.Background(), tnmEditor(), domain.Term{
					ID: tnmStoredID[tax], Name: tnmStoredName, Slug: c.slug,
					Taxonomy: tax, ParentID: c.parent})
				if err != nil {
					t.Fatalf("update: %v", err)
				}
				if w.updated == nil || w.updated.Name != tnmStoredName {
					t.Fatalf("updated = %+v, want Name %q unchanged", w.updated, tnmStoredName)
				}
			})
		}
	}
}

func TestTermNameUpdateStoredReadFailure(t *testing.T) { // 6.3
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			boom := errors.New("read boom")
			w.readErr = boom
			err := tnmSvc(t, w).Update(context.Background(), tnmEditor(), domain.Term{
				ID: tnmStoredID[tax], Name: "Renamed", Slug: "news", Taxonomy: tax})
			if !errors.Is(err, boom) {
				t.Fatalf("err = %v, want the read error", err)
			}
			if tnmWrites(w) {
				t.Errorf("writer called: %+v", w.updated)
			}
		})
	}
}

func TestTermNameUpdateReadsStoredExactlyOnce(t *testing.T) { // 6.4
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			err := tnmSvc(t, w).Update(context.Background(), tnmEditor(), domain.Term{
				ID: tnmStoredID[tax], Name: "Renamed", Slug: "news", Taxonomy: tax})
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			if w.reads != 1 {
				t.Errorf("stored reads = %d, want 1", w.reads)
			}
			if len(w.lastIDsArg) != 1 || w.lastIDsArg[0] != tnmStoredID[tax] {
				t.Errorf("read ids = %v, want [%d]", w.lastIDsArg, tnmStoredID[tax])
			}
		})
	}
}

func TestTermNameUnauthorizedBeforeAnyReadOrName(t *testing.T) { // 5.8
	for _, tax := range tnmTaxonomies {
		t.Run(tax, func(t *testing.T) {
			w := tnmNewWriter()
			svc := tnmSvc(t, w)
			_, cerr := svc.Create(context.Background(), tnmNobody(),
				domain.Term{Name: "<script>x</script>", Slug: "x", Taxonomy: tax})
			uerr := svc.Update(context.Background(), tnmNobody(), domain.Term{
				ID: tnmStoredID[tax], Name: "<script>x</script>", Slug: "news", Taxonomy: tax})
			if !errors.Is(cerr, ErrForbidden) || !errors.Is(uerr, ErrForbidden) {
				t.Fatalf("create err = %v, update err = %v, want ErrForbidden", cerr, uerr)
			}
			if w.reads != 0 || tnmWrites(w) {
				t.Errorf("reads = %d, writes = %v, want none", w.reads, tnmWrites(w))
			}
		})
	}
}

func TestTermNameBuiltinSlugsAcceptedAsBefore(t *testing.T) { // 5.6
	for _, tax := range []string{TaxonomyCategory, taxonomyPostTag} {
		for _, slug := range []string{"News", "caf%C3%A9", "c1", "news"} { // upper, percent-encoded, duplicate siblings
			t.Run(tax+"/"+slug, func(t *testing.T) {
				w := tnmNewWriter()
				_, err := tnmSvc(t, w).Create(context.Background(), tnmEditor(),
					domain.Term{Name: "Fresh", Slug: slug, Taxonomy: tax})
				if err != nil {
					t.Fatalf("create: %v", err)
				}
				if w.created == nil || w.created.Slug != slug {
					t.Fatalf("created = %+v, want Slug %q verbatim", w.created, slug)
				}
			})
		}
	}
}

func TestTermNameRegisteredSlugRulesStillApply(t *testing.T) { // 5.7
	w := tnmNewWriter()
	svc := tnmSvc(t, w)
	_, err := svc.Create(context.Background(), tnmEditor(),
		domain.Term{Name: "Fine", Slug: "Bad Slug", Taxonomy: "genre"})
	if !errors.Is(err, ErrTermSlugInvalid) {
		t.Errorf("charset: err = %v, want ErrTermSlugInvalid", err)
	}
	_, err = svc.Create(context.Background(), tnmEditor(),
		domain.Term{Name: "Fine", Slug: "g1", Taxonomy: "genre"})
	if !errors.Is(err, ErrTermSlugTaken) {
		t.Errorf("sibling: err = %v, want ErrTermSlugTaken", err)
	}
	if tnmWrites(w) {
		t.Errorf("writer called: %+v", w.created)
	}
}
