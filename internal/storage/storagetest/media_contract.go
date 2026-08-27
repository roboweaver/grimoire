package storagetest

import (
	"context"
	"errors"
	"reflect"
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
		// A no-op SetParent (same parent id it already has) must still
		// succeed rather than report ErrNotFound (Req 12 MySQL parity — see
		// the matching comment in comments_contract.go).
		if err := repos.MediaWriter.SetParent(ctx, id, 5); err != nil {
			t.Fatalf("SetParent no-op: %v", err)
		}
		noop, err := repos.Media.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID after no-op SetParent: %v", err)
		}
		if noop.ParentID != 5 {
			t.Fatalf("parent after no-op SetParent = %d, want 5", noop.ParentID)
		}
		if err := repos.MediaWriter.SetParent(ctx, 9999, 1); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("SetParent missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("MediaRepository filters by search, type, and date range", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()
		ctx := context.Background()
		cases := []struct {
			name   string
			filter domain.MediaFilter
			wantID []int64
		}{
			{
				name:   "search matches filename case-insensitively",
				filter: domain.MediaFilter{Search: "jpg", Limit: 10},
				wantID: []int64{201},
			},
			{
				name:   "search matches title case-insensitively",
				filter: domain.MediaFilter{Search: "ASSET", Limit: 10},
				wantID: []int64{202},
			},
			{
				name:   "type image matches both jpeg and png",
				filter: domain.MediaFilter{Type: "image", Limit: 10},
				wantID: []int64{202, 201},
			},
			{
				name:   "type video matches nothing",
				filter: domain.MediaFilter{Type: "video", Limit: 10},
				wantID: []int64{},
			},
			{
				name:   "type document matches nothing (no non-media attachments seeded)",
				filter: domain.MediaFilter{Type: "document", Limit: 10},
				wantID: []int64{},
			},
			{
				name:   "after excludes the earlier attachment",
				filter: domain.MediaFilter{After: mustParseDate(t, "2024-01-06T12:00:00Z"), Limit: 10},
				wantID: []int64{202},
			},
			{
				name:   "before excludes the later attachment",
				filter: domain.MediaFilter{Before: mustParseDate(t, "2024-01-06T00:00:00Z"), Limit: 10},
				wantID: []int64{201},
			},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				list, err := repos.Media.List(ctx, tc.filter)
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				count, err := repos.Media.Count(ctx, tc.filter)
				if err != nil {
					t.Fatalf("Count: %v", err)
				}
				if len(list) != count {
					t.Fatalf("len(list)=%d != count=%d — listQuery and Count predicates diverged", len(list), count)
				}
				gotID := make([]int64, len(list))
				for i, m := range list {
					gotID[i] = m.ID
				}
				if !reflect.DeepEqual(gotID, tc.wantID) {
					t.Fatalf("got IDs %v, want %v", gotID, tc.wantID)
				}
			})
		}
	})

	t.Run("MediaRepository type filter matches MIME case-insensitively", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		id, err := repos.MediaWriter.Create(ctx, domain.Media{
			Title:    "Uppercase MIME Upload",
			Filename: "2024/02/upper.JPG",
			URL:      "/wp-content/uploads/2024/02/upper.JPG",
			MimeType: "IMAGE/JPEG",
			Date:     time.Date(2024, 2, 4, 5, 6, 7, 0, time.UTC),
			ParentID: 0,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		list, err := repos.Media.List(ctx, domain.MediaFilter{Type: "image", Limit: 10})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		count, err := repos.Media.Count(ctx, domain.MediaFilter{Type: "image", Limit: 10})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if len(list) != count {
			t.Fatalf("len(list)=%d != count=%d — listQuery and Count predicates diverged", len(list), count)
		}
		gotID := make([]int64, len(list))
		for i, m := range list {
			gotID[i] = m.ID
		}
		wantID := []int64{id, 202, 201}
		if !reflect.DeepEqual(gotID, wantID) {
			t.Fatalf("got IDs %v, want %v", gotID, wantID)
		}
	})
}

func mustParseDate(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("mustParseDate(%q): %v", s, err)
	}
	return ts
}
