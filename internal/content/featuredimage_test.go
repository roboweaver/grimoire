package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakePostMetaRepo struct {
	featuredMediaID map[int64]int64
	err             error
}

func (f *fakePostMetaRepo) FeaturedMediaID(ctx context.Context, postID int64) (int64, error) {
	if f.err != nil {
		return 0, f.err
	}
	return f.featuredMediaID[postID], nil
}

func (f *fakePostMetaRepo) AttachmentMetadata(ctx context.Context, postID int64) (string, error) {
	return "", nil
}

func TestFeaturedImageServiceURL(t *testing.T) {
	meta := &fakePostMetaRepo{featuredMediaID: map[int64]int64{42: 100}}
	media := &fakeMediaRepo{byID: map[int64]domain.Media{100: {ID: 100, URL: "/wp-content/uploads/2026/07/foo.png"}}}
	svc := NewFeaturedImageService(meta, media)

	if got := svc.URL(context.Background(), 42); got != "/wp-content/uploads/2026/07/foo.png" {
		t.Fatalf("URL = %q, want the attachment URL", got)
	}
}

// TestFeaturedImageServiceURLAbsentAsEmpty covers every case where a
// featured image should be treated as simply not present, rather than an
// error that breaks the page render: no thumbnail set, a stale thumbnail ID
// pointing at a deleted attachment, and a nil service (unwired dependency).
func TestFeaturedImageServiceURLAbsentAsEmpty(t *testing.T) {
	t.Run("no thumbnail set", func(t *testing.T) {
		meta := &fakePostMetaRepo{}
		media := &fakeMediaRepo{}
		svc := NewFeaturedImageService(meta, media)
		if got := svc.URL(context.Background(), 42); got != "" {
			t.Fatalf("URL = %q, want empty", got)
		}
	})

	t.Run("stale thumbnail ID", func(t *testing.T) {
		meta := &fakePostMetaRepo{featuredMediaID: map[int64]int64{42: 999}}
		media := &fakeMediaRepo{}
		svc := NewFeaturedImageService(meta, media)
		if got := svc.URL(context.Background(), 42); got != "" {
			t.Fatalf("URL = %q, want empty", got)
		}
	})

	t.Run("postmeta error", func(t *testing.T) {
		meta := &fakePostMetaRepo{err: errors.New("boom")}
		media := &fakeMediaRepo{}
		svc := NewFeaturedImageService(meta, media)
		if got := svc.URL(context.Background(), 42); got != "" {
			t.Fatalf("URL = %q, want empty", got)
		}
	})

	t.Run("nil service", func(t *testing.T) {
		var svc *FeaturedImageService
		if got := svc.URL(context.Background(), 42); got != "" {
			t.Fatalf("URL = %q, want empty", got)
		}
	})
}
