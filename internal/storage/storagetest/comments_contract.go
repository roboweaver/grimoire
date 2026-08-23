package storagetest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

func runCommentsContract(t *testing.T, newRepos NewReposFunc) {
	t.Helper()
	ctx := context.Background()

	t.Run("CommentRepository list count and by-id", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		comments, err := repos.Comments.List(ctx, domain.CommentFilter{PostID: 1, Statuses: []string{"1"}})
		if err != nil {
			t.Fatalf("List approved by post: %v", err)
		}
		if len(comments) != 1 || comments[0].ID != 101 {
			t.Fatalf("approved comments = %+v, want [101]", comments)
		}
		all, err := repos.Comments.List(ctx, domain.CommentFilter{Limit: 10})
		if err != nil {
			t.Fatalf("List all: %v", err)
		}
		if len(all) != 4 {
			t.Fatalf("all comments len = %d, want 4", len(all))
		}
		count, err := repos.Comments.Count(ctx, domain.CommentFilter{Statuses: []string{"0", "1", "spam", "trash"}})
		if err != nil {
			t.Fatalf("Count: %v", err)
		}
		if count != 4 {
			t.Fatalf("count = %d, want 4", count)
		}
		got, err := repos.Comments.ByID(ctx, 103)
		if err != nil {
			t.Fatalf("ByID: %v", err)
		}
		if got.Status != "spam" || got.PostID != 2 {
			t.Fatalf("ByID got %+v", got)
		}
		if _, err := repos.Comments.ByID(ctx, 9999); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("ByID missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("CommentWriter create defaults and update status", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		id, err := repos.CommentWriter.Create(ctx, domain.Comment{
			PostID:      1,
			Author:      "Queue Tester",
			AuthorEmail: "queue@example.com",
			AuthorURL:   "https://example.com",
			AuthorIP:    "203.0.113.5",
			Date:        time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			DateGMT:     time.Date(2024, 1, 5, 10, 0, 0, 0, time.UTC),
			Content:     "pending moderation",
			Status:      "0",
			Agent:       "test-agent",
			Parent:      0,
			UserID:      0,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created, err := repos.Comments.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID created: %v", err)
		}
		if created.Status != "0" || created.Author != "Queue Tester" {
			t.Fatalf("created = %+v", created)
		}
		if err := repos.CommentWriter.UpdateStatus(ctx, id, "1"); err != nil {
			t.Fatalf("UpdateStatus: %v", err)
		}
		updated, err := repos.Comments.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID updated: %v", err)
		}
		if updated.Status != "1" {
			t.Fatalf("updated status = %q, want 1", updated.Status)
		}
		// A no-op update (setting the status to the value it already holds)
		// must still succeed rather than report ErrNotFound: some vendor
		// drivers (MySQL, without CLIENT_FOUND_ROWS) report RowsAffected as
		// "rows changed", not "rows matched", so a naive RowsAffected==0
		// check would misread this as a missing row (Req 12 parity).
		if err := repos.CommentWriter.UpdateStatus(ctx, id, "1"); err != nil {
			t.Fatalf("UpdateStatus no-op: %v", err)
		}
		noop, err := repos.Comments.ByID(ctx, id)
		if err != nil {
			t.Fatalf("ByID after no-op update: %v", err)
		}
		if noop.Status != "1" {
			t.Fatalf("status after no-op update = %q, want 1", noop.Status)
		}
		if err := repos.CommentWriter.UpdateStatus(ctx, 9999, "spam"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("UpdateStatus missing err = %v, want ErrNotFound", err)
		}
	})

	t.Run("CommentMetaRepository round-trip", func(t *testing.T) {
		repos, cleanup := newRepos(t)
		defer cleanup()

		if err := repos.CommentMeta.Set(ctx, 101, "_wp_trash_meta_status", "1"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := repos.CommentMeta.Set(ctx, 101, "_wp_trash_meta_time", "1700000000"); err != nil {
			t.Fatalf("Set time: %v", err)
		}
		v, err := repos.CommentMeta.Get(ctx, 101, "_wp_trash_meta_status")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if v != "1" {
			t.Fatalf("Get = %q, want 1", v)
		}
		all, err := repos.CommentMeta.ByComment(ctx, 101)
		if err != nil {
			t.Fatalf("ByComment: %v", err)
		}
		if all["_seed"] != "comment-101" || all["_wp_trash_meta_time"] != "1700000000" {
			t.Fatalf("ByComment = %+v", all)
		}
		if err := repos.CommentMeta.Delete(ctx, 101, "_wp_trash_meta_time"); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := repos.CommentMeta.Get(ctx, 101, "_wp_trash_meta_time"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("Get deleted err = %v, want ErrNotFound", err)
		}
	})
}
