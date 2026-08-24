package storagetest

import (
	"context"
	"testing"
)

// runPostMetaContract covers PostMetaRepository: FeaturedMediaID (seeded via
// "_thumbnail_id", zero when unset) and AttachmentMetadata (seeded via
// "_wp_attachment_metadata", "" when unset).
func runPostMetaContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("FeaturedMediaID returns the seeded thumbnail ID", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		id, err := repos.PostMeta.FeaturedMediaID(ctx, 3)
		if err != nil {
			t.Fatalf("FeaturedMediaID: %v", err)
		}
		if id != 201 {
			t.Errorf("FeaturedMediaID(3) = %d, want 201", id)
		}
	})

	t.Run("FeaturedMediaID is zero when unset", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		id, err := repos.PostMeta.FeaturedMediaID(ctx, 2)
		if err != nil {
			t.Fatalf("FeaturedMediaID: %v", err)
		}
		if id != 0 {
			t.Errorf("FeaturedMediaID(2) = %d, want 0", id)
		}
	})

	t.Run("AttachmentMetadata returns the seeded raw value", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		meta, err := repos.PostMeta.AttachmentMetadata(ctx, 201)
		if err != nil {
			t.Fatalf("AttachmentMetadata: %v", err)
		}
		want := `a:2:{s:5:"width";i:800;s:6:"height";i:600;}`
		if meta != want {
			t.Errorf("AttachmentMetadata(201) = %q, want %q", meta, want)
		}
	})

	t.Run("AttachmentMetadata is empty when unset", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		meta, err := repos.PostMeta.AttachmentMetadata(ctx, 202)
		if err != nil {
			t.Fatalf("AttachmentMetadata: %v", err)
		}
		if meta != "" {
			t.Errorf("AttachmentMetadata(202) = %q, want empty", meta)
		}
	})
}
