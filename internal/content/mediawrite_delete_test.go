package content

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// TestMediaWriteDeleteOwnershipGate covers Req 3.1/3.2/3.4: an own-attachment
// delete needs delete_posts; an others'-attachment delete needs
// delete_others_posts; a caller without the needed cap gets ErrForbidden and
// nothing is deleted.
func TestMediaWriteDeleteOwnershipGate(t *testing.T) {
	t.Run("own attachment allowed with delete_posts", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 42, 7, domain.Media{ID: 42, AuthorID: 7, Filename: "2024/05/x.png"})
		// author has delete_posts for own content.
		if _, err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 7), 42); err != nil {
			t.Fatalf("own delete: %v", err)
		}
		if writer.deleted != 42 {
			t.Errorf("writer.Delete not called with id 42; got %d", writer.deleted)
		}
	})

	t.Run("others attachment forbidden without delete_others_posts", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 43, 999, domain.Media{ID: 43, AuthorID: 999, Filename: "2024/05/y.png"})
		// contributor has delete_posts but NOT delete_others_posts.
		_, err := svc.Delete(context.Background(), actor(auth.RoleContributor, 7), 43)
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("others delete without cap = %v, want ErrForbidden", err)
		}
		if writer.deleted != 0 {
			t.Errorf("forbidden delete must not call writer.Delete; got id %d", writer.deleted)
		}
	})

	t.Run("others attachment allowed with delete_others_posts", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 44, 999, domain.Media{ID: 44, AuthorID: 999, Filename: "2024/05/z.png"})
		if _, err := svc.Delete(context.Background(), actor(auth.RoleEditor, 7), 44); err != nil {
			t.Fatalf("editor others delete: %v", err)
		}
		if writer.deleted != 44 {
			t.Errorf("writer.Delete not called with id 44; got %d", writer.deleted)
		}
	})
}

// TestMediaWriteDeleteNotFound covers Req 1.4/4.4: an unknown id bubbles
// domain.ErrNotFound and nothing is deleted.
func TestMediaWriteDeleteNotFound(t *testing.T) {
	svc, _, writer := newMediaWriteUpdateForTest(t)
	_, err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), 12345)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id delete = %v, want domain.ErrNotFound", err)
	}
	if writer.deleted != 0 {
		t.Errorf("not-found delete must not call writer.Delete")
	}
}

// TestMediaWriteDeletePreviousAndFileUnlink covers Req 1.3/4.2/4.3: the returned
// previous equals the stored row (captured before delete), the on-disk file is
// unlinked, and a pre-removed file is non-fatal.
func TestMediaWriteDeletePreviousAndFileUnlink(t *testing.T) {
	t.Run("file unlinked, previous returned", func(t *testing.T) {
		svc, repo, _ := newMediaWriteUpdateForTest(t)
		rel := "2024/05/deleteme.png"
		stageStoredMedia(repo, 50, 7, domain.Media{ID: 50, AuthorID: 7, Filename: rel})
		// create the on-disk file under the service's uploads dir
		full := filepath.Join(svc.media.Config().UploadsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("png"), 0o644); err != nil {
			t.Fatal(err)
		}
		prev, err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 7), 50)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if prev.ID != 50 || prev.Filename != rel {
			t.Errorf("previous = %+v, want the stored row (id 50, %q)", prev, rel)
		}
		if _, err := os.Stat(full); !os.IsNotExist(err) {
			t.Errorf("on-disk file still present after delete: %v", err)
		}
	})

	t.Run("missing file is non-fatal", func(t *testing.T) {
		svc, repo, _ := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 51, 7, domain.Media{ID: 51, AuthorID: 7, Filename: "2024/05/gone.png"})
		// do NOT create the file; delete must still succeed.
		if _, err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 7), 51); err != nil {
			t.Fatalf("delete with missing file = %v, want nil (non-fatal)", err)
		}
	})
}

// TestMediaWriteDeleteDBFirst covers the DB-delete-first order: when the writer
// delete fails, the error is returned and the on-disk file is left untouched.
func TestMediaWriteDeleteDBFirst(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	rel := "2024/05/keep.png"
	stageStoredMedia(repo, 60, 7, domain.Media{ID: 60, AuthorID: 7, Filename: rel})
	full := filepath.Join(svc.media.Config().UploadsDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer.deleteErr = errors.New("db boom")
	if _, err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 7), 60); err == nil {
		t.Fatalf("delete with writer error = nil, want error")
	}
	if _, err := os.Stat(full); err != nil {
		t.Errorf("file must survive a failed DB delete (DB-delete-first): %v", err)
	}
}
