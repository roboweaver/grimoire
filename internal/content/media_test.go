package content

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

type fakeMediaRepo struct {
	list    []domain.Media
	count   int
	byID    map[int64]domain.Media
	listErr error
	byIDErr map[int64]error
	filter  domain.MediaFilter
}

func (f *fakeMediaRepo) List(_ context.Context, filter domain.MediaFilter) ([]domain.Media, error) {
	f.filter = filter
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]domain.Media, len(f.list))
	copy(out, f.list)
	return out, nil
}

func (f *fakeMediaRepo) Count(_ context.Context, filter domain.MediaFilter) (int, error) {
	f.filter = filter
	return f.count, nil
}

func (f *fakeMediaRepo) ByID(_ context.Context, id int64) (domain.Media, error) {
	if err := f.byIDErr[id]; err != nil {
		return domain.Media{}, err
	}
	m, ok := f.byID[id]
	if !ok {
		return domain.Media{}, domain.ErrNotFound
	}
	return m, nil
}

type fakeMediaWriter struct {
	created   []domain.Media
	setParent []struct{ id, parentID int64 }
	createID  int64
	createErr error
	parentErr error
}

func (f *fakeMediaWriter) Create(_ context.Context, m domain.Media) (int64, error) {
	f.created = append(f.created, m)
	if f.createErr != nil {
		return 0, f.createErr
	}
	if f.createID == 0 {
		f.createID = 88
	}
	return f.createID, nil
}

func (f *fakeMediaWriter) SetParent(_ context.Context, id, parentID int64) error {
	f.setParent = append(f.setParent, struct{ id, parentID int64 }{id: id, parentID: parentID})
	return f.parentErr
}

func TestMediaServiceStoreWritesAttachmentAndMetadata(t *testing.T) {
	root := t.TempDir()
	writer := &fakeMediaWriter{}
	svc := NewMediaService(&fakeMediaRepo{}, writer, MediaConfig{UploadsDir: root, BaseURL: "/wp-content/uploads", AllowedMIMEs: []string{"image/png"}})
	svc.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }

	media, err := svc.Store(context.Background(), bytes.NewReader([]byte("pngdata")), MediaUpload{
		Filename: "My Cat!!.png",
		MimeType: "image/png",
		Title:    "",
	})
	if err != nil {
		t.Fatalf("Store: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("created attachments = %d, want 1", len(writer.created))
	}
	got := writer.created[0]
	if got.Filename != "2026/08/my-cat.png" {
		t.Fatalf("stored filename = %q, want 2026/08/my-cat.png", got.Filename)
	}
	if got.URL != "/wp-content/uploads/2026/08/my-cat.png" {
		t.Fatalf("stored url = %q", got.URL)
	}
	if got.Title != "My Cat" {
		t.Fatalf("stored title = %q, want My Cat", got.Title)
	}
	if _, err := os.Stat(filepath.Join(root, "2026", "08", "my-cat.png")); err != nil {
		t.Fatalf("stored file missing: %v", err)
	}
	if media.ID != 88 {
		t.Fatalf("media ID = %d, want 88", media.ID)
	}
}

func TestMediaServiceStoreDeduplicatesAndRollsBackDBFailure(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "2026", "08"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "2026", "08", "image.png"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := &fakeMediaWriter{createErr: errors.New("db boom")}
	svc := NewMediaService(&fakeMediaRepo{}, writer, MediaConfig{UploadsDir: root, BaseURL: "/wp-content/uploads", AllowedMIMEs: []string{"image/png"}})
	svc.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }

	_, err := svc.Store(context.Background(), bytes.NewReader([]byte("new")), MediaUpload{Filename: "image.png", MimeType: "image/png"})
	if err == nil {
		t.Fatal("expected DB failure")
	}
	if len(writer.created) != 1 || writer.created[0].Filename != "2026/08/image-1.png" {
		t.Fatalf("dedup filename = %+v", writer.created)
	}
	if _, statErr := os.Stat(filepath.Join(root, "2026", "08", "image-1.png")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("rollback should delete written file, stat err = %v", statErr)
	}
}

func TestMediaServiceStoreDoesNotInsertOnFileWriteFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "uploads")
	if err := os.WriteFile(root, []byte("not-a-dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer := &fakeMediaWriter{}
	svc := NewMediaService(&fakeMediaRepo{}, writer, MediaConfig{UploadsDir: root, BaseURL: "/wp-content/uploads", AllowedMIMEs: []string{"image/png"}})
	svc.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }

	_, err := svc.Store(context.Background(), bytes.NewReader([]byte("new")), MediaUpload{Filename: "image.png", MimeType: "image/png"})
	if err == nil {
		t.Fatal("expected file write failure")
	}
	if len(writer.created) != 0 {
		t.Fatalf("db insert should not run on file write failure: %+v", writer.created)
	}
}
