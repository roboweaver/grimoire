package content

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// TDD RED for M12 task 11.3. It asserts the REST view model of design
// Component 6c, which task 11.4 adds to rest.go:
//
//	type RESTCustomPost struct {
//		restItemCommon
//		Terms map[string][]int64 // key: taxonomy rest_base
//	}
//	func (p RESTCustomPost) MarshalJSON() ([]byte, error)
//	func (m *RESTMapper) WithRegistry(r *registry.Registry) *RESTMapper
//	func (m *RESTMapper) CustomPost(ctx context.Context, p domain.Post,
//		pt registry.PostType, ancestry []string) (RESTCustomPost, error)
//
// Expected JSON for a book with taxonomies category (rest_base "categories"),
// shelf (rest_base "shelf") and genre (rest_base "genres"):
//
//	{"id":..,"type":"book","link":"/books/dune/",...,
//	 "categories":[],"shelf":[3,4],"genres":[9]}
//
// Taxonomies not attached to the type produce no key (no stray categories/tags).
// Taxonomy rest_base collisions with common keys are rejected by the registry
// and are not tested here.

const restCustomStruct = "/blog/%year%/%monthnum%/%postname%/"

func restCustomFixture(t *testing.T) (*RESTMapper, *registry.Registry, *fakeRESTPostTerms, *fakeRESTPostMeta) {
	t.Helper()
	st, err := routing.Parse(restCustomStruct, "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	no := false
	cfg := config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "book", Slug: "books", WithFront: &no, Taxonomies: []string{"category", "shelf", "genre"}},
			{Name: "manual", Slug: "manuals", Hierarchical: true, Taxonomies: []string{"post_tag"}},
			{Name: "note", Slug: "notes", WithFront: &no},
			{Name: "secret", Public: &no, Taxonomies: []string{"shelf"}},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "shelf", Slug: "shelf", WithFront: &no},
			{Name: "genre", Slug: "genre", RESTBase: "genres", WithFront: &no},
		},
	}
	reg, err := registry.Build(cfg, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	types, taxes := reg.Routes()
	st = st.WithCustomTypes(types, taxes)

	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	meta := &fakeRESTPostMeta{featured: map[int64]int64{}, attach: map[int64]string{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	m := NewRESTMapper(terms, meta, users, "wp_").WithPermalinks(st).WithRegistry(reg)
	return m, reg, terms, meta
}

func restCustomRow(name, slug string, id int64) domain.Post {
	p := samplePost()
	p.ID = id
	p.Type = name
	p.Slug = slug
	return p
}

func restCustomJSON(t *testing.T, v RESTCustomPost) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("Unmarshal %s: %v", b, err)
	}
	return out
}

func restCustomPT(t *testing.T, reg *registry.Registry, name string) registry.PostType {
	t.Helper()
	pt, ok := reg.PostType(name)
	if !ok {
		t.Fatalf("post type %q not registered", name)
	}
	return pt
}

func TestRESTCustomPostTermArraysKeyedByRESTBase(t *testing.T) {
	m, reg, terms, meta := restCustomFixture(t)
	terms.byPost[7] = map[string][]int64{"shelf": {3, 4}, "genre": {9}}
	meta.featured[7] = 55

	got, err := m.CustomPost(context.Background(), restCustomRow("book", "dune", 7), restCustomPT(t, reg, "book"), []string{"dune"})
	if err != nil {
		t.Fatalf("CustomPost: %v", err)
	}
	obj := restCustomJSON(t, got)

	for key, want := range map[string]string{
		"shelf":          "[3,4]",
		"genres":         "[9]",
		"categories":     "[]", // attached but empty: [] not null
		"type":           `"book"`,
		"id":             "7",
		"featured_media": "55",
	} {
		if g := string(obj[key]); g != want {
			t.Errorf("%s = %s, want %s", key, g, want)
		}
	}
	if _, ok := obj["genre"]; ok {
		t.Errorf("term array keyed by taxonomy name %q, want rest_base %q", "genre", "genres")
	}
	if _, ok := obj["tags"]; ok {
		t.Errorf("unattached post_tag emitted a tags key: %s", obj["tags"])
	}
	for _, k := range []string{"date", "slug", "status", "link", "guid", "title", "content", "excerpt", "author", "comment_status", "ping_status"} {
		if _, ok := obj[k]; !ok {
			t.Errorf("common field %q missing from flattened JSON", k)
		}
	}
}

func TestRESTCustomPostNoCategoriesOrTagsUnlessAttached(t *testing.T) {
	m, reg, _, _ := restCustomFixture(t)
	got, err := m.CustomPost(context.Background(), restCustomRow("note", "n1", 8), restCustomPT(t, reg, "note"), []string{"n1"})
	if err != nil {
		t.Fatalf("CustomPost: %v", err)
	}
	obj := restCustomJSON(t, got)
	for _, k := range []string{"categories", "tags", "shelf", "genres"} {
		if _, ok := obj[k]; ok {
			t.Errorf("type with no taxonomies emitted key %q: %s", k, obj[k])
		}
	}
}

func TestRESTCustomPostBuiltInTagsWhenAttached(t *testing.T) {
	m, reg, terms, _ := restCustomFixture(t)
	terms.byPost[9] = map[string][]int64{"post_tag": {5}}
	got, err := m.CustomPost(context.Background(), restCustomRow("manual", "guide", 9), restCustomPT(t, reg, "manual"), []string{"guide"})
	if err != nil {
		t.Fatalf("CustomPost: %v", err)
	}
	obj := restCustomJSON(t, got)
	if g := string(obj["tags"]); g != "[5]" {
		t.Errorf("tags = %s, want [5]", g)
	}
	if _, ok := obj["categories"]; ok {
		t.Errorf("unattached category emitted a categories key")
	}
}

func TestRESTCustomPostLink(t *testing.T) {
	m, reg, _, _ := restCustomFixture(t)
	ctx := context.Background()
	tests := []struct {
		name     string
		typ      string
		ancestry []string
		want     string
	}{
		{"flat without front", "book", []string{"dune"}, "/books/dune/"},
		{"hierarchical with front, nested", "manual", []string{"guide", "install"}, "/blog/manuals/guide/install/"},
		{"unrouted public=false falls back to plain permalink", "secret", []string{"s1"}, "/?p=11&post_type=secret"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			slug := tc.ancestry[len(tc.ancestry)-1]
			got, err := m.CustomPost(ctx, restCustomRow(tc.typ, slug, 11), restCustomPT(t, reg, tc.typ), tc.ancestry)
			if err != nil {
				t.Fatalf("CustomPost: %v", err)
			}
			if got.Link == "" {
				t.Fatalf("link is empty; restAbs(\"\") would be the site root")
			}
			if got.Link != tc.want {
				t.Errorf("link = %q, want %q", got.Link, tc.want)
			}
			if got.Type != tc.typ {
				t.Errorf("type = %q, want %q", got.Type, tc.typ)
			}
			if strings.Contains(got.Link, "/2024/") {
				t.Errorf("link %q is the dated post shape (Canonical leaked)", got.Link)
			}
		})
	}
}

func TestRESTCustomPostEmptyTermsMarshalAsArray(t *testing.T) {
	m, reg, _, _ := restCustomFixture(t)
	got, err := m.CustomPost(context.Background(), restCustomRow("book", "dune", 7), restCustomPT(t, reg, "book"), []string{"dune"})
	if err != nil {
		t.Fatalf("CustomPost: %v", err)
	}
	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, k := range []string{"categories", "shelf", "genres"} {
		if !strings.Contains(string(b), `"`+k+`":[]`) {
			t.Errorf("%s not marshalled as []: %s", k, b)
		}
	}
	if strings.Contains(string(b), "null") && strings.Contains(string(b), ":null") {
		t.Errorf("JSON contains null: %s", b)
	}
}

// With the registry wired, a post and a page link are unchanged (Canonical and
// the M15 page guard remain the only construction sites for those shapes).
func TestRESTCustomRegistryLeavesPostAndPageLinksUnchanged(t *testing.T) {
	m, _, _, _ := restCustomFixture(t)
	ctx := context.Background()

	post, err := m.Post(ctx, samplePost())
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	if want := "/blog/2024/03/hello-world/"; post.Link != want {
		t.Errorf("post link = %q, want %q", post.Link, want)
	}

	m = m.WithPages(restPageService())
	page, err := m.Page(ctx, restNestedPage())
	if err != nil {
		t.Fatalf("Page: %v", err)
	}
	if want := "/handbook/onboarding/"; page.Link != want {
		t.Errorf("nested page link = %q, want %q", page.Link, want)
	}
}
