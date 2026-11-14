package config

import (
	"reflect"
	"strings"
	"testing"
)

const contentDBHeader = `
database:
  vendor: sqlite
  dsn: "file:grimoire.db"
`

func loadContent(t *testing.T, extra string) (Config, error) {
	t.Helper()
	return Load(writeTempConfig(t, contentDBHeader+extra))
}

func mustLoadContent(t *testing.T, extra string) Config {
	t.Helper()
	cfg, err := loadContent(t, extra)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func boolVal(t *testing.T, name string, p *bool) bool {
	t.Helper()
	if p == nil {
		t.Fatalf("%s is nil after defaults; want non-nil", name)
	}
	return *p
}

func TestContentPostTypeDefaults(t *testing.T) {
	cfg := mustLoadContent(t, `
content:
  post_types:
    - name: book
`)
	if len(cfg.Content.PostTypes) != 1 {
		t.Fatalf("post types = %d, want 1", len(cfg.Content.PostTypes))
	}
	pt := cfg.Content.PostTypes[0]
	if pt.Name != "book" {
		t.Errorf("name = %q", pt.Name)
	}
	if !boolVal(t, "Public", pt.Public) {
		t.Error("public default = false, want true")
	}
	if pt.RESTBase != "book" {
		t.Errorf("rest_base = %q, want book", pt.RESTBase)
	}
	if pt.HasArchive != (HasArchive{}) {
		t.Errorf("has_archive = %+v, want zero (false)", pt.HasArchive)
	}
	if pt.Slug != "book" {
		t.Errorf("slug = %q, want book", pt.Slug)
	}
	if !boolVal(t, "WithFront", pt.WithFront) {
		t.Error("with_front default = false, want true")
	}
	if pt.Hierarchical {
		t.Error("hierarchical default = true, want false")
	}
	if pt.CapabilityType != "post" {
		t.Errorf("capability_type = %q, want post", pt.CapabilityType)
	}
	if len(pt.Taxonomies) != 0 {
		t.Errorf("taxonomies = %v, want empty", pt.Taxonomies)
	}
}

func TestContentTaxonomyDefaults(t *testing.T) {
	cfg := mustLoadContent(t, `
content:
  taxonomies:
    - name: shelf
`)
	if len(cfg.Content.Taxonomies) != 1 {
		t.Fatalf("taxonomies = %d, want 1", len(cfg.Content.Taxonomies))
	}
	tx := cfg.Content.Taxonomies[0]
	if tx.Name != "shelf" {
		t.Errorf("name = %q", tx.Name)
	}
	if !boolVal(t, "Public", tx.Public) {
		t.Error("public default = false, want true")
	}
	if tx.RESTBase != "shelf" {
		t.Errorf("rest_base = %q, want shelf", tx.RESTBase)
	}
	if tx.Slug != "shelf" {
		t.Errorf("slug = %q, want shelf", tx.Slug)
	}
	if !boolVal(t, "WithFront", tx.WithFront) {
		t.Error("with_front default = false, want true")
	}
	if tx.Hierarchical {
		t.Error("hierarchical default = true, want false")
	}
	if len(tx.ObjectTypes) != 0 {
		t.Errorf("object_types = %v, want empty", tx.ObjectTypes)
	}
}

func TestContentExplicitValuesSurviveDefaulting(t *testing.T) {
	cfg := mustLoadContent(t, `
content:
  post_types:
    - name: book
      public: false
      with_front: false
      rest_base: books_api
      slug: library
      hierarchical: true
      capability_type: event
      taxonomies: [shelf]
  taxonomies:
    - name: shelf
      public: false
      with_front: false
      rest_base: shelves_api
      slug: shelves
      hierarchical: true
      object_types: [book]
`)
	pt := cfg.Content.PostTypes[0]
	if boolVal(t, "pt.Public", pt.Public) {
		t.Error("explicit public:false was overwritten with true")
	}
	if boolVal(t, "pt.WithFront", pt.WithFront) {
		t.Error("explicit with_front:false was overwritten with true")
	}
	if pt.RESTBase != "books_api" || pt.Slug != "library" || pt.CapabilityType != "event" || !pt.Hierarchical {
		t.Errorf("explicit post type fields lost: %+v", pt)
	}
	if !reflect.DeepEqual(pt.Taxonomies, []string{"shelf"}) {
		t.Errorf("taxonomies = %v", pt.Taxonomies)
	}
	tx := cfg.Content.Taxonomies[0]
	if boolVal(t, "tx.Public", tx.Public) {
		t.Error("explicit taxonomy public:false was overwritten with true")
	}
	if boolVal(t, "tx.WithFront", tx.WithFront) {
		t.Error("explicit taxonomy with_front:false was overwritten with true")
	}
	if tx.RESTBase != "shelves_api" || tx.Slug != "shelves" || !tx.Hierarchical {
		t.Errorf("explicit taxonomy fields lost: %+v", tx)
	}
	if !reflect.DeepEqual(tx.ObjectTypes, []string{"book"}) {
		t.Errorf("object_types = %v", tx.ObjectTypes)
	}
}

func TestContentExplicitTrueAlsoKept(t *testing.T) {
	cfg := mustLoadContent(t, `
content:
  post_types:
    - name: book
      public: true
      with_front: true
`)
	pt := cfg.Content.PostTypes[0]
	if !boolVal(t, "Public", pt.Public) || !boolVal(t, "WithFront", pt.WithFront) {
		t.Errorf("explicit true lost: %+v", pt)
	}
}

func TestContentHasArchiveForms(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  HasArchive
	}{
		{"omitted", "", HasArchive{}},
		{"true", "has_archive: true", HasArchive{Enabled: true}},
		{"false", "has_archive: false", HasArchive{}},
		{"slug string", "has_archive: library", HasArchive{Enabled: true, Slug: "library"}},
		{"quoted slug string", `has_archive: "my-library"`, HasArchive{Enabled: true, Slug: "my-library"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := mustLoadContent(t, "\ncontent:\n  post_types:\n    - name: book\n      "+tc.value+"\n")
			if got := cfg.Content.PostTypes[0].HasArchive; got != tc.want {
				t.Errorf("HasArchive = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestContentHasArchiveRejectsBooleanLookingStrings(t *testing.T) {
	values := []string{
		"yes", "no", "on", "off", "y", "n",
		"Yes", "NO", "On", "OFF", "Y", "N",
		`"true"`, `"false"`, `"True"`, `"FALSE"`, `"TRUE"`,
		`"yes"`, `"off"`,
	}
	for _, v := range values {
		t.Run(v, func(t *testing.T) {
			_, err := loadContent(t, "\ncontent:\n  post_types:\n    - name: book\n      has_archive: "+v+"\n")
			if err == nil {
				t.Fatalf("has_archive: %s loaded; want error", v)
			}
		})
	}
}

func TestContentMisspeltKeyIsLoadError(t *testing.T) {
	_, err := loadContent(t, `
content:
  post_types:
    - name: book
      hierarchial: true
`)
	if err == nil {
		t.Fatal("misspelt key inside content: loaded; want error")
	}
	if !strings.Contains(err.Error(), "hierarchial") {
		t.Errorf("error %q should name the field hierarchial", err.Error())
	}
}

func TestContentMisspeltTaxonomyAndTopLevelKeysAreLoadErrors(t *testing.T) {
	for name, body := range map[string]string{
		"taxonomy key": "\ncontent:\n  taxonomies:\n    - name: shelf\n      slugg: x\n",
		"content key":  "\ncontent:\n  post_typez: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadContent(t, body); err == nil {
				t.Fatal("loaded; want error")
			}
		})
	}
}

func TestContentStrictnessDoesNotLeakToOtherSections(t *testing.T) {
	cfg := mustLoadContent(t, `
server:
  addr: ":9090"
  not_a_real_key: whatever
`)
	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr = %q, want :9090", cfg.Server.Addr)
	}
}

func TestContentAbsentSectionIsZero(t *testing.T) {
	cfg := mustLoadContent(t, "")
	if !reflect.DeepEqual(cfg.Content, ContentConfig{}) {
		t.Errorf("Content = %+v, want zero ContentConfig", cfg.Content)
	}
	if cfg.Content.PostTypes != nil || cfg.Content.Taxonomies != nil {
		t.Errorf("slices should be nil: %+v", cfg.Content)
	}
}

func TestContentEmptyNameStaysEmptyAndNoCrossEntryCheck(t *testing.T) {
	cfg := mustLoadContent(t, `
content:
  post_types:
    - public: true
    - name: dup
    - name: dup
  taxonomies:
    - hierarchical: true
`)
	pts := cfg.Content.PostTypes
	if len(pts) != 3 {
		t.Fatalf("post types = %d, want 3 (no cross-entry rejection)", len(pts))
	}
	if pts[0].Name != "" || pts[0].Slug != "" || pts[0].RESTBase != "" {
		t.Errorf("empty name must leave Slug/RESTBase empty: %+v", pts[0])
	}
	if pts[0].CapabilityType != "post" {
		t.Errorf("scalar defaults still apply to nameless entry: %+v", pts[0])
	}
	if pts[1].Slug != "dup" || pts[2].Slug != "dup" {
		t.Errorf("duplicates kept and defaulted: %+v %+v", pts[1], pts[2])
	}
	tx := cfg.Content.Taxonomies[0]
	if tx.Name != "" || tx.Slug != "" || tx.RESTBase != "" {
		t.Errorf("empty taxonomy name must leave Slug/RESTBase empty: %+v", tx)
	}
}
