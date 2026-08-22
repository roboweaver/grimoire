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
