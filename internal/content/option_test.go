package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeOptionRepo struct {
	values map[string]string
	err    error
}

func (f *fakeOptionRepo) Get(ctx context.Context, name string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.values[name]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

func TestOptionServiceGetPresentAndAbsent(t *testing.T) {
	repo := &fakeOptionRepo{values: map[string]string{"blogname": "grimoire"}}
	svc := NewOptionService(repo)

	if got := svc.Get(context.Background(), "blogname"); got != "grimoire" {
		t.Fatalf("present = %q, want grimoire", got)
	}
	if got := svc.Get(context.Background(), "missing"); got != "" {
		t.Fatalf("absent = %q, want empty", got)
	}
}

func TestOptionServiceSiteInfo(t *testing.T) {
	repo := &fakeOptionRepo{values: map[string]string{
		"blogname":        "grimoire",
		"blogdescription": "A Go-native CMS",
	}}
	svc := NewOptionService(repo)

	title, tagline := svc.SiteInfo(context.Background())
	if title != "grimoire" || tagline != "A Go-native CMS" {
		t.Fatalf("SiteInfo = %q / %q", title, tagline)
	}
}

// TestOptionServiceSiteInfoDecodesHTMLEntities: WordPress stores option
// values with special characters already HTML-entity-encoded at rest (e.g.
// blogdescription = "Weaver&#039;s Loom"). SiteInfo must decode these so
// html/template (which only escapes, never decodes) renders the intended
// apostrophe instead of the literal entity text.
func TestOptionServiceSiteInfoDecodesHTMLEntities(t *testing.T) {
	repo := &fakeOptionRepo{values: map[string]string{
		"blogname":        "Weaver&#039;s Site",
		"blogdescription": "Weaver&#039;s Loom: Technology &amp; musings",
	}}
	svc := NewOptionService(repo)

	title, tagline := svc.SiteInfo(context.Background())
	if title != "Weaver's Site" {
		t.Fatalf("title = %q, want decoded apostrophe", title)
	}
	if tagline != "Weaver's Loom: Technology & musings" {
		t.Fatalf("tagline = %q, want decoded entities", tagline)
	}
}

// TestOptionServiceBaseURLs verifies siteurl/home are trimmed, deduped, and
// each expanded to both http/https variants so content authored under either
// scheme can be rewritten.
func TestOptionServiceBaseURLs(t *testing.T) {
	repo := &fakeOptionRepo{values: map[string]string{
		"siteurl": "http://127.0.0.1:8080/",
		"home":    "http://127.0.0.1:8080",
	}}
	svc := NewOptionService(repo)

	got := svc.BaseURLs(context.Background())
	want := []string{"http://127.0.0.1:8080", "https://127.0.0.1:8080"}
	if len(got) != len(want) {
		t.Fatalf("BaseURLs = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("BaseURLs[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestOptionServiceBaseURLsEmptyWhenUnset confirms a missing siteurl/home
// yields an empty (not nil-panicking) slice.
func TestOptionServiceBaseURLsEmptyWhenUnset(t *testing.T) {
	svc := NewOptionService(&fakeOptionRepo{values: map[string]string{}})
	if got := svc.BaseURLs(context.Background()); len(got) != 0 {
		t.Fatalf("BaseURLs = %v, want empty", got)
	}
}
