package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

func runMediaContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("MediaRepository list count by-id and parent filter", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		items, err := repos.Media.List(ctx, domain.MediaFilter{Limit: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(items) != 2 || items[0].ID != 202 || items[1].ID != 201 {
			t.Fatalf("items = %+v, want [202 201] newest-first", items)
		}
		attached, err := repos.Media.List(ctx, domain.MediaFilter{ParentID: 1, Limit: 10})
		if err != nil {
			t.Fatalf("List parent: %v", err)
		}
		if len(attached) != 1 || attached[0].ID != 201 {
			t.Fatalf("attached = %+v, want [201]", attached)
		}
		count, err := repos.Media.Count(ctx, domain.MediaFilter{})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if count != 2 {
			t.Fatalf("count = %d, want 2", count)
		}
		got, err := repos.Media.ByID(ctx, 201)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Filename != "2024/01/photo.jpg" || got.ParentID != 1 {
			t.Fatalf("ByID = %+v", got)
		}
	})

	t.Run("MediaWriter create and set parent", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		id, err := repos.MediaWriter.Create(ctx, domain.Media{
			Title:    "Contract Upload",
			Filename: "2024/02/upload.png",
			URL:      "/wp-content/uploads/2024/02/upload.png",
			MimeType: "image/png",
			Date:     time.Date(2024, 2, 3, 4, 5, 6, 0, time.UTC),
			ParentID: 0,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created, err := repos.Media.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID created: %v", err)
		}
		if created.MimeType != "image/png" || created.Filename != "2024/02/upload.png" {
			t.Fatalf("created = %+v", created)
		}
		if err := repos.MediaWriter.SetParent(ctx, id, 5); err != nil {
			t.Fatalf("SetParent: %v", err)
		}
		updated, err := repos.Media.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID updated: %v", err)
		}
		if updated.ParentID != 5 {
			t.Fatalf("parent = %d, want 5", updated.ParentID)
		}
		if err := repos.MediaWriter.SetParent(ctx, 9999, 1); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetParent missing err = %v, want ErrNotFound", err)
		}
	})
}
