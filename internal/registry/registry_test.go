package registry_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

func mustBuild(t *testing.T, cfg config.ContentConfig) *registry.Registry {
	t.Helper()
	reg, err := registry.Build(cfg, structure(t, "/blog/%postname%/", "", ""))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return reg
}

func ptNames(r *registry.Registry) []string {
	var out []string
	for _, p := range r.PostTypes() {
		out = append(out, p.Name)
	}
	return out
}

func TestRegistryNilReceiverSafe(t *testing.T) {
	var r *registry.Registry
	if !r.Empty() {
		t.Error("nil Empty() = false, want true")
	}
	if got := r.PostTypes(); len(got) != 0 {
		t.Errorf("nil PostTypes() = %v", got)
	}
	if got := r.Taxonomies(); len(got) != 0 {
		t.Errorf("nil Taxonomies() = %v", got)
	}
	if _, ok := r.PostType("book"); ok {
		t.Error("nil PostType ok = true")
	}
	if _, ok := r.Taxonomy("shelf"); ok {
		t.Error("nil Taxonomy ok = true")
	}
	if _, ok := r.PostTypeByRESTBase("books"); ok {
		t.Error("nil PostTypeByRESTBase ok = true")
	}
	if _, ok := r.TaxonomyByRESTBase("shelves"); ok {
		t.Error("nil TaxonomyByRESTBase ok = true")
	}
	if base, ok := r.TaxonomyRESTBase("shelf"); ok || base != "" {
		t.Errorf("nil TaxonomyRESTBase = %q, %v", base, ok)
	}
	if tr, xr := r.Routes(); len(tr) != 0 || len(xr) != 0 {
		t.Errorf("nil Routes() = %v, %v", tr, xr)
	}
	if got := r.Notes(); len(got) != 0 {
		t.Errorf("nil Notes() = %v", got)
	}
}

func TestRegistryAbsentContentIsEmptyNonNil(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{})
	if reg == nil {
		t.Fatal("Build(empty) returned nil registry")
	}
	if !reg.Empty() {
		t.Error("Empty() = false, want true")
	}
	if len(reg.PostTypes()) != 0 || len(reg.Taxonomies()) != 0 {
		t.Error("empty registry lists entries")
	}
	if tr, xr := reg.Routes(); len(tr) != 0 || len(xr) != 0 {
		t.Errorf("Routes() = %v, %v", tr, xr)
	}
	if _, ok := reg.PostType("book"); ok {
		t.Error("PostType ok = true on empty registry")
	}
}

func TestRegistryUnionEitherDirection(t *testing.T) {
	cases := map[string]config.ContentConfig{
		"declared on post type": {
			PostTypes:  []config.PostTypeConfig{pt("book", ptTaxes("shelf"))},
			Taxonomies: []config.TaxonomyConfig{tx("shelf")},
		},
		"declared on taxonomy": {
			PostTypes:  []config.PostTypeConfig{pt("book")},
			Taxonomies: []config.TaxonomyConfig{tx("shelf", txObjects("book"))},
		},
		"declared on both, deduplicated": {
			PostTypes:  []config.PostTypeConfig{pt("book", ptTaxes("shelf", "shelf"))},
			Taxonomies: []config.TaxonomyConfig{tx("shelf", txObjects("book", "book"))},
		},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			reg := mustBuild(t, cfg)
			book, ok := reg.PostType("book")
			if !ok || !reflect.DeepEqual(book.Taxonomies, []string{"shelf"}) {
				t.Errorf("book.Taxonomies = %v (ok=%v), want [shelf]", book.Taxonomies, ok)
			}
			shelf, ok := reg.Taxonomy("shelf")
			if !ok || !reflect.DeepEqual(shelf.ObjectTypes, []string{"book"}) {
				t.Errorf("shelf.ObjectTypes = %v (ok=%v), want [book]", shelf.ObjectTypes, ok)
			}
		})
	}
}

func TestRegistryUnionDeclarationOrder(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			pt("book", ptTaxes("genre")),
			pt("film"),
		},
		Taxonomies: []config.TaxonomyConfig{
			tx("shelf", txObjects("book")),
			tx("genre", txObjects("film", "book")),
		},
	})
	book, _ := reg.PostType("book")
	if want := []string{"genre", "shelf"}; !reflect.DeepEqual(book.Taxonomies, want) {
		t.Errorf("book.Taxonomies = %v, want %v", book.Taxonomies, want)
	}
	genre, _ := reg.Taxonomy("genre")
	if want := []string{"film", "book"}; !reflect.DeepEqual(genre.ObjectTypes, want) {
		t.Errorf("genre.ObjectTypes = %v, want %v", genre.ObjectTypes, want)
	}
}

func TestRegistryLookupsAndRESTBase(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{pt("book", ptRest("books"))},
		Taxonomies: []config.TaxonomyConfig{tx("shelf", txRest("shelves"))},
	})
	if p, ok := reg.PostTypeByRESTBase("books"); !ok || p.Name != "book" {
		t.Errorf("PostTypeByRESTBase(books) = %+v, %v", p, ok)
	}
	if _, ok := reg.PostTypeByRESTBase("book"); ok {
		t.Error("PostTypeByRESTBase matched a name, not a rest_base")
	}
	if x, ok := reg.TaxonomyByRESTBase("shelves"); !ok || x.Name != "shelf" {
		t.Errorf("TaxonomyByRESTBase(shelves) = %+v, %v", x, ok)
	}
	if _, ok := reg.PostType("nope"); ok {
		t.Error("PostType(nope) ok = true")
	}
	if _, ok := reg.Taxonomy("nope"); ok {
		t.Error("Taxonomy(nope) ok = true")
	}
	for name, want := range map[string]string{
		"category": "categories",
		"post_tag": "tags",
		"shelf":    "shelves",
	} {
		if got, ok := reg.TaxonomyRESTBase(name); !ok || got != want {
			t.Errorf("TaxonomyRESTBase(%q) = %q, %v; want %q", name, got, ok, want)
		}
	}
	if got, ok := reg.TaxonomyRESTBase("nope"); ok || got != "" {
		t.Errorf("TaxonomyRESTBase(nope) = %q, %v", got, ok)
	}
}

func TestRegistryArchiveResolution(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{PostTypes: []config.PostTypeConfig{
		pt("book", ptSlug("books")),
		pt("film", ptArchiveTrue(), ptSlug("films")),
		pt("song", ptArchive("tunes")),
		pt("secret", ptArchive("hidden"), ptPrivate()),
	}})
	book, _ := reg.PostType("book")
	if book.HasArchive() || book.ArchiveSlug != "" {
		t.Errorf("book: HasArchive=%v ArchiveSlug=%q", book.HasArchive(), book.ArchiveSlug)
	}
	film, _ := reg.PostType("film")
	if !film.HasArchive() || film.ArchiveSlug != "films" {
		t.Errorf("film: HasArchive=%v ArchiveSlug=%q, want rewrite slug", film.HasArchive(), film.ArchiveSlug)
	}
	song, _ := reg.PostType("song")
	if !song.HasArchive() || song.ArchiveSlug != "tunes" {
		t.Errorf("song: HasArchive=%v ArchiveSlug=%q, want tunes", song.HasArchive(), song.ArchiveSlug)
	}
	secret, _ := reg.PostType("secret")
	if secret.HasArchive() {
		t.Error("secret (public:false): HasArchive() = true, want false")
	}
}

func TestPostTypeHeading(t *testing.T) {
	for name, want := range map[string]string{
		"book":         "Book",
		"press-kit":    "Press kit",
		"case_study":   "Case study",
		"a-b_c":        "A b c",
		"_leading":     " leading",
		"already-done": "Already done",
	} {
		if got := (registry.PostType{Name: name}).Heading(); got != want {
			t.Errorf("Heading(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRegistryRoutesPublicOnly(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			pt("book", ptSlug("books"), ptArchiveTrue(), ptNoFront()),
			pt("secret", ptPrivate()),
			pt("doc", ptSlug("docs"), func(c *config.PostTypeConfig) { c.Hierarchical = true }),
		},
		Taxonomies: []config.TaxonomyConfig{
			tx("shelf", txSlug("shelves"), func(c *config.TaxonomyConfig) { c.Hierarchical = true }),
			tx("hidden", txPrivate()),
			tx("mood", txNoFront()),
		},
	})
	types, taxes := reg.Routes()
	wantTypes := []routing.TypeRoute{
		{Name: "book", Slug: "books", ArchiveSlug: "books", WithFront: false, Hierarchical: false},
		{Name: "doc", Slug: "docs", WithFront: true, Hierarchical: true},
	}
	wantTaxes := []routing.TaxRoute{
		{Name: "shelf", Slug: "shelves", WithFront: true, Hierarchical: true},
		{Name: "mood", Slug: "mood", WithFront: false},
	}
	if !reflect.DeepEqual(types, wantTypes) {
		t.Errorf("type routes = %+v\nwant %+v", types, wantTypes)
	}
	if !reflect.DeepEqual(taxes, wantTaxes) {
		t.Errorf("tax routes = %+v\nwant %+v", taxes, wantTaxes)
	}
}

func TestRegistryRoutesClassify(t *testing.T) {
	st := structure(t, "/blog/%postname%/", "", "")
	reg, err := registry.Build(config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{pt("book", ptSlug("books"), ptArchiveTrue())},
		Taxonomies: []config.TaxonomyConfig{tx("shelf", txSlug("shelves"))},
	}, st)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	types, taxes := reg.Routes()
	cs := st.WithCustomTypes(types, taxes)
	for path, want := range map[string]struct {
		kind routing.Kind
		name string
	}{
		"/blog/books/":      {routing.KindCustomArchive, "book"},
		"/blog/books/dune/": {routing.KindCustomPost, "book"},
		"/blog/shelves/sf/": {routing.KindCustomTerm, "shelf"},
	} {
		got := cs.Classify(path)
		if got.Kind != want.kind || got.TypeName != want.name {
			t.Errorf("Classify(%q) = kind %v type %q; want kind %v type %q",
				path, got.Kind, got.TypeName, want.kind, want.name)
		}
	}
}

func TestRegistryNotesUnattachedTaxonomy(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{
		PostTypes: []config.PostTypeConfig{pt("book", ptTaxes("shelf"))},
		Taxonomies: []config.TaxonomyConfig{
			tx("shelf"),
			tx("orphan"),
		},
	})
	if reg == nil {
		t.Fatal("non-fatal observation must not fail Build")
	}
	notes := reg.Notes()
	if len(notes) != 1 || !strings.Contains(notes[0], "orphan") {
		t.Errorf("Notes() = %v, want exactly one note naming orphan", notes)
	}
	attached := mustBuild(t, config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{pt("book")},
		Taxonomies: []config.TaxonomyConfig{tx("shelf", txObjects("book"))},
	})
	if got := attached.Notes(); len(got) != 0 {
		t.Errorf("Notes() = %v, want none", got)
	}
}

// Req 1.6 / D2: the registry holds exactly the configured names. Build takes no
// database handle, so nothing else can add an entry.
func TestRegistryNeverGrowsBeyondConfig(t *testing.T) {
	reg := mustBuild(t, config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{pt("book"), pt("film")},
		Taxonomies: []config.TaxonomyConfig{tx("shelf")},
	})
	if got, want := ptNames(reg), []string{"book", "film"}; !reflect.DeepEqual(got, want) {
		t.Errorf("PostTypes() names = %v, want %v", got, want)
	}
	if got := reg.Taxonomies(); len(got) != 1 || got[0].Name != "shelf" {
		t.Errorf("Taxonomies() = %+v, want only shelf", got)
	}
	if reg.Empty() {
		t.Error("Empty() = true on a populated registry")
	}
}
