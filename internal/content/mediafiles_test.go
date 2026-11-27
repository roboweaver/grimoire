package content

// RED tests for the media file cleanup helpers (plans/23-media-delete-and-
// purge-config tasks 4.1(b) and 5.1). They fail to compile until
// mediaFileCandidates, filterSharedThumb, deleteMediaFiles and the
// removeMediaFile deletion seam exist in internal/content/mediafiles.go.
//
// Contract assumed by these tests (the implementation must provide it):
//
//	var removeMediaFile = os.Remove                         // narrow unlink seam
//	func mediaFileCandidates(metaRaw, backupRaw string) []string
//	func filterSharedThumb(ctx, meta, id, thumb, candidates) []string
//	func deleteMediaFiles(cfg MediaConfig, mainFile string, candidates []string, log *slog.Logger)
//
// filterSharedThumb's meta argument must be satisfied by mfMetaFake, i.e. a
// narrow reader with ThumbSharedElsewhere(ctx, excludeID, filename) (bool, error)
// (optionally combined with the domain.PostMetaRepository methods).
// Candidates are relative to the main file's directory, as in WordPress's
// wp_delete_attachment_files.

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"testing/quick"

	"github.com/roboweaver/grimoire/internal/php"
)

// ---------------------------------------------------------------- helpers

// mfMetaFake is the "other attachments' metadata" lookup.
type mfMetaFake struct {
	shared map[string]bool
	meta   string
	backup string
}

func (f mfMetaFake) FeaturedMediaID(context.Context, int64) (int64, error) { return 0, nil }
func (f mfMetaFake) AttachmentMetadata(context.Context, int64) (string, error) {
	return f.meta, nil
}
func (f mfMetaFake) BackupSizes(context.Context, int64) (string, error) { return f.backup, nil }
func (f mfMetaFake) ThumbSharedElsewhere(_ context.Context, _ int64, name string) (bool, error) {
	return f.shared[name], nil
}

func mfSerialize(t testing.TB, v any) string {
	t.Helper()
	s, err := php.Serialize(v)
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return s
}

func mfSorted(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}

func mfWrite(t testing.TB, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func mfExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// mfResolve resolves symlinks, falling back to the nearest existing ancestor
// for a path that does not exist.
func mfResolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	dir := filepath.Dir(p)
	if dir == p {
		return p
	}
	return filepath.Join(mfResolve(dir), filepath.Base(p))
}

func mfWithin(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// mfSeam replaces the unlink seam for the test. do may be nil (real
// os.Remove). It returns the recorded attempts (in call order).
func mfSeam(t testing.TB, do func(path string) error) *[]string {
	t.Helper()
	var attempts []string
	orig := removeMediaFile
	removeMediaFile = func(path string) error {
		attempts = append(attempts, path)
		if do != nil {
			return do(path)
		}
		return os.Remove(path)
	}
	t.Cleanup(func() { removeMediaFile = orig })
	return &attempts
}

// mfTree is a temp uploads dir with an outside dir beside it.
type mfTree struct {
	uploads, outside string
}

func mfNewTree(t testing.TB) mfTree {
	t.Helper()
	base := t.TempDir()
	tr := mfTree{uploads: filepath.Join(base, "uploads"), outside: filepath.Join(base, "outside")}
	for _, d := range []string{tr.uploads, tr.outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}
	return tr
}

func (tr mfTree) cfg() MediaConfig { return MediaConfig{UploadsDir: tr.uploads} }

// ------------------------------------------- mediaFileCandidates examples

func TestMediaFileCandidatesEmptyAndMalformedGiveNone(t *testing.T) {
	for name, tc := range map[string]struct{ meta, backup string }{
		"both empty":      {"", ""},
		"malformed meta":  {"a:1:{s:5:\"sizes\"", ""},
		"garbage backup":  {"", "not serialized"},
		"both malformed":  {"{{{", "i:"},
		"scalar payloads": {"i:5;", "s:1:\"x\";"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mediaFileCandidates(tc.meta, tc.backup); len(got) != 0 {
				t.Fatalf("candidates = %v, want none", got)
			}
		})
	}
}

func TestMediaFileCandidatesSizesGiveEveryFile(t *testing.T) {
	meta := mfSerialize(t, map[string]any{
		"file":   "2024/05/photo.jpg",
		"width":  1000,
		"height": 800,
		"sizes": map[string]any{
			"thumbnail": map[string]any{"file": "photo-150x150.jpg", "width": 150},
			"medium":    map[string]any{"file": "photo-300x240.jpg", "width": 300},
		},
	})
	got := mfSorted(mediaFileCandidates(meta, ""))
	want := []string{"photo-150x150.jpg", "photo-300x240.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v (the main 'file' key is not a candidate)", got, want)
	}
}

func TestMediaFileCandidatesNamedKeysContributeWhenNonEmpty(t *testing.T) {
	for _, key := range []string{"original_image", "source_image", "animated_video", "animated_video_poster", "thumb"} {
		t.Run(key+" present", func(t *testing.T) {
			meta := mfSerialize(t, map[string]any{key: "named-" + key + ".bin"})
			got := mediaFileCandidates(meta, "")
			if !reflect.DeepEqual(got, []string{"named-" + key + ".bin"}) {
				t.Fatalf("candidates = %v, want just the %s file", got, key)
			}
		})
		t.Run(key+" empty", func(t *testing.T) {
			meta := mfSerialize(t, map[string]any{key: ""})
			if got := mediaFileCandidates(meta, ""); len(got) != 0 {
				t.Fatalf("empty %s gave candidates %v", key, got)
			}
		})
	}
}

func TestMediaFileCandidatesBackupSizesContributeFiles(t *testing.T) {
	backup := mfSerialize(t, map[string]any{
		"full-orig":      map[string]any{"file": "photo-orig.jpg", "width": 2000, "height": 1500},
		"thumbnail-orig": map[string]any{"file": "photo-150x150-orig.jpg", "width": 150},
	})
	got := mfSorted(mediaFileCandidates("", backup))
	want := []string{"photo-150x150-orig.jpg", "photo-orig.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestMediaFileCandidatesOriginalImageAndBackupTogether(t *testing.T) {
	meta := mfSerialize(t, map[string]any{"original_image": "photo-scaled-orig.jpg"})
	backup := mfSerialize(t, map[string]any{"full-orig": map[string]any{"file": "photo-backup.jpg"}})
	got := mfSorted(mediaFileCandidates(meta, backup))
	want := []string{"photo-backup.jpg", "photo-scaled-orig.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

func TestMediaFileCandidatesMalformedMetaStillReadsBackup(t *testing.T) {
	backup := mfSerialize(t, map[string]any{"full-orig": map[string]any{"file": "photo-backup.jpg"}})
	got := mediaFileCandidates("{{{", backup)
	if !reflect.DeepEqual(got, []string{"photo-backup.jpg"}) {
		t.Fatalf("candidates = %v, want the backup file only", got)
	}
}

// ------------------------------------------------ filterSharedThumb examples

func TestFilterSharedThumbDropsSharedThumbOnly(t *testing.T) {
	cands := []string{"photo-150x150.jpg", "thumb.jpg", "photo-orig.jpg"}
	f := mfMetaFake{shared: map[string]bool{"thumb.jpg": true}}
	got := mfSorted(filterSharedThumb(context.Background(), f, 7, "thumb.jpg", cands))
	want := []string{"photo-150x150.jpg", "photo-orig.jpg"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}
}

func TestFilterSharedThumbKeepsUnsharedThumb(t *testing.T) {
	cands := []string{"photo-150x150.jpg", "thumb.jpg"}
	got := mfSorted(filterSharedThumb(context.Background(), mfMetaFake{}, 7, "thumb.jpg", cands))
	if !reflect.DeepEqual(got, mfSorted(cands)) {
		t.Fatalf("filtered = %v, want all of %v", got, cands)
	}
}

// ---------------------------------------- Property 2: candidates match WP

// mfGenMeta builds a random metadata/backup payload plus the expected set.
func mfGenMeta(t testing.TB, rng *rand.Rand) (metaRaw, backupRaw string, shared map[string]bool, want []string) {
	n := 0
	name := func(p string) string { n++; return p + "-" + strconv.Itoa(n) + ".jpg" }
	meta := map[string]any{"file": "2024/05/main.jpg", "width": rng.Intn(4000)}
	var all []string
	add := func(v string) { all = append(all, v) }

	if rng.Intn(4) != 0 {
		sizes := map[string]any{}
		for i := rng.Intn(5); i > 0; i-- {
			f := name("size")
			sizes["s"+strconv.Itoa(i)] = map[string]any{"file": f, "width": 100 * i}
			add(f)
		}
		meta["sizes"] = sizes
	}
	for _, k := range []string{"original_image", "source_image", "animated_video", "animated_video_poster"} {
		switch rng.Intn(3) {
		case 0:
			f := name(k)
			meta[k] = f
			add(f)
		case 1:
			meta[k] = "" // present but empty: not a candidate
		}
	}
	shared = map[string]bool{}
	if rng.Intn(2) == 0 {
		th := name("thumb")
		meta["thumb"] = th
		if rng.Intn(2) == 0 {
			shared[th] = true
		} else {
			add(th)
		}
	}
	backup := map[string]any{}
	for i := rng.Intn(4); i > 0; i-- {
		f := name("bak")
		backup["b"+strconv.Itoa(i)] = map[string]any{"file": f, "width": 10 * i}
		add(f)
	}
	if rng.Intn(5) != 0 {
		metaRaw = mfSerialize(t, meta)
	}
	if len(backup) > 0 {
		backupRaw = mfSerialize(t, backup)
	}
	if metaRaw == "" {
		// meta absent: only the backup files remain, and no thumb/size is read
		all = nil
		shared = map[string]bool{}
		for _, v := range mediaBackupFiles(backup) {
			all = append(all, v)
		}
	}
	return metaRaw, backupRaw, shared, all
}

func mediaBackupFiles(b map[string]any) []string {
	var out []string
	for _, v := range b {
		out = append(out, v.(map[string]any)["file"].(string))
	}
	return out
}

// TestPropertyMediaFileCandidates checks Property 2.
//
// Feature: media-delete-and-purge-config, Property 2: Media file candidates match WordPress's own set
//
// Validates: Requirements 2.1, 2.2, 2.3, 2.4, 2.5, 2.6, 2.7, 2.8
func TestPropertyMediaFileCandidates(t *testing.T) {
	prop := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		metaRaw, backupRaw, shared, want := mfGenMeta(t, rng)
		got := filterSharedThumb(context.Background(), mfMetaFake{shared: shared}, 7, mediaThumbName(metaRaw),
			mediaFileCandidates(metaRaw, backupRaw))
		if !reflect.DeepEqual(mfSorted(got), mfSorted(want)) {
			t.Logf("seed %d: got %v want %v (meta %q backup %q)", seed, mfSorted(got), mfSorted(want), metaRaw, backupRaw)
			return false
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(23))}); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------- deleteMediaFiles examples

func TestDeleteMediaFilesPlainRelativeCandidateDeletes(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"photo-150x150.jpg"}, nil)

	for _, f := range []string{"photo.jpg", "photo-150x150.jpg"} {
		if mfExists(filepath.Join(tr.uploads, f)) {
			t.Errorf("%s still exists", f)
		}
	}
}

func TestDeleteMediaFilesCandidatesResolveAgainstMainFileDir(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "2024/05/photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "2024/05/photo-150x150.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg")) // wrong dir: must survive

	deleteMediaFiles(tr.cfg(), "2024/05/photo.jpg", []string{"photo-150x150.jpg"}, nil)

	if mfExists(filepath.Join(tr.uploads, "2024/05/photo.jpg")) || mfExists(filepath.Join(tr.uploads, "2024/05/photo-150x150.jpg")) {
		t.Error("files under the main file's directory were not removed")
	}
	if !mfExists(filepath.Join(tr.uploads, "photo-150x150.jpg")) {
		t.Error("a same-named file in the uploads root was removed")
	}
}

func TestDeleteMediaFilesDotDotEscapeIsSkipped(t *testing.T) {
	tr := mfNewTree(t)
	victim := filepath.Join(tr.outside, "victim.jpg")
	mfWrite(t, victim)
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg"))
	attempts := mfSeam(t, nil)

	deleteMediaFiles(tr.cfg(), "photo.jpg",
		[]string{"../outside/victim.jpg", "photo-150x150.jpg"}, nil)

	if !mfExists(victim) {
		t.Fatal("file outside the uploads dir was deleted via ..")
	}
	for _, a := range *attempts {
		if !mfWithin(mfResolve(tr.uploads), mfResolve(a)) {
			t.Errorf("unlink attempted outside the uploads dir: %s", a)
		}
	}
	if mfExists(filepath.Join(tr.uploads, "photo.jpg")) || mfExists(filepath.Join(tr.uploads, "photo-150x150.jpg")) {
		t.Error("in-bounds candidates were not removed after an out-of-bounds one")
	}
}

func TestDeleteMediaFilesDotDotInsideTreeIsAllowed(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "keep/../sibling.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"keep/../sibling.jpg"}, nil)

	if mfExists(filepath.Join(tr.uploads, "sibling.jpg")) {
		t.Error("a .. candidate that stays inside the tree was not deleted")
	}
}

func TestDeleteMediaFilesAbsoluteOutsideCandidateIsSkipped(t *testing.T) {
	tr := mfNewTree(t)
	victim := filepath.Join(tr.outside, "victim.jpg")
	mfWrite(t, victim)
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg"))
	attempts := mfSeam(t, nil)

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{victim, "photo-150x150.jpg"}, nil)

	if !mfExists(victim) {
		t.Fatal("absolute path outside the uploads dir was deleted")
	}
	for _, a := range *attempts {
		if mfResolve(a) == mfResolve(victim) {
			t.Errorf("unlink attempted on the outside file: %s", a)
		}
	}
	if mfExists(filepath.Join(tr.uploads, "photo-150x150.jpg")) {
		t.Error("in-bounds candidate was not removed after an absolute escape")
	}
}

func TestDeleteMediaFilesSymlinkDirEscapeIsSkipped(t *testing.T) {
	tr := mfNewTree(t)
	victim := filepath.Join(tr.outside, "victim.jpg")
	mfWrite(t, victim)
	if err := os.Symlink(tr.outside, filepath.Join(tr.uploads, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"link/victim.jpg", "photo-150x150.jpg"}, nil)

	if !mfExists(victim) {
		t.Fatal("file reached through an escaping directory symlink was deleted")
	}
	if mfExists(filepath.Join(tr.uploads, "photo-150x150.jpg")) {
		t.Error("in-bounds candidate was not removed after a symlink escape")
	}
}

func TestDeleteMediaFilesSymlinkFileEscapeIsSkipped(t *testing.T) {
	tr := mfNewTree(t)
	victim := filepath.Join(tr.outside, "victim.jpg")
	mfWrite(t, victim)
	if err := os.Symlink(victim, filepath.Join(tr.uploads, "leaf.jpg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"leaf.jpg"}, nil)

	if !mfExists(victim) {
		t.Fatal("file reached through an escaping file symlink was deleted")
	}
}

func TestDeleteMediaFilesSymlinkInsideTreeIsAllowed(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "real/x.jpg"))
	if err := os.Symlink(filepath.Join(tr.uploads, "real"), filepath.Join(tr.uploads, "alias")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"alias/x.jpg"}, nil)

	if mfExists(filepath.Join(tr.uploads, "real/x.jpg")) {
		t.Error("a symlink that stays inside the tree blocked deletion")
	}
}

func TestDeleteMediaFilesMainFileEscapeIsSkipped(t *testing.T) {
	tr := mfNewTree(t)
	victim := filepath.Join(tr.outside, "victim.jpg")
	mfWrite(t, victim)

	deleteMediaFiles(tr.cfg(), "../outside/victim.jpg", nil, nil)

	if !mfExists(victim) {
		t.Fatal("main file outside the uploads dir was deleted")
	}
}

func TestDeleteMediaFilesMissingCandidateIsNotAnError(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "photo.jpg"))
	mfWrite(t, filepath.Join(tr.uploads, "after.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"gone-1.jpg", "sub/gone-2.jpg", "after.jpg"}, slog.New(slog.NewTextHandler(os.Stderr, nil)))

	if mfExists(filepath.Join(tr.uploads, "photo.jpg")) || mfExists(filepath.Join(tr.uploads, "after.jpg")) {
		t.Error("existing files were not removed around missing candidates")
	}
}

func TestDeleteMediaFilesMissingMainFileStillRemovesCandidates(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "photo-150x150.jpg"))

	deleteMediaFiles(tr.cfg(), "photo.jpg", []string{"photo-150x150.jpg"}, nil)

	if mfExists(filepath.Join(tr.uploads, "photo-150x150.jpg")) {
		t.Error("candidate not removed when the main file is already gone")
	}
}

func TestDeleteMediaFilesUnlinkFailureDoesNotStopRemainingCandidates(t *testing.T) {
	tr := mfNewTree(t)
	names := []string{"main.jpg", "a.jpg", "b.jpg", "c.jpg", "d.jpg"}
	for _, n := range names {
		mfWrite(t, filepath.Join(tr.uploads, n))
	}
	failing := filepath.Join(mfResolve(tr.uploads), "b.jpg")
	attempts := mfSeam(t, func(p string) error {
		if mfResolve(p) == failing {
			return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrPermission}
		}
		return os.Remove(p)
	})

	deleteMediaFiles(tr.cfg(), "main.jpg", []string{"a.jpg", "b.jpg", "c.jpg", "d.jpg"}, nil)

	if len(*attempts) != len(names) {
		t.Fatalf("attempts = %v, want one per file (%d)", *attempts, len(names))
	}
	for _, n := range []string{"main.jpg", "a.jpg", "c.jpg", "d.jpg"} {
		if mfExists(filepath.Join(tr.uploads, n)) {
			t.Errorf("%s was not removed after an earlier unlink failure", n)
		}
	}
	if !mfExists(filepath.Join(tr.uploads, "b.jpg")) {
		t.Error("b.jpg should remain (its unlink failed)")
	}
}

func TestDeleteMediaFilesNilLoggerWithFailureDoesNotPanic(t *testing.T) {
	tr := mfNewTree(t)
	mfWrite(t, filepath.Join(tr.uploads, "main.jpg"))
	mfSeam(t, func(string) error { return errors.New("boom") })
	deleteMediaFiles(tr.cfg(), "main.jpg", []string{"x.jpg"}, nil)
}

// ----------------------- Property 3: deletion never escapes the uploads dir

type mfShape struct {
	cand       string
	definiteIn bool   // must be attempted, resolved to wantResolved
	wantPath   string // resolved path expected when definiteIn
}

// TestPropertyFileDeletionStaysInUploadsDir checks Property 3.
//
// Feature: media-delete-and-purge-config, Property 3: File deletion never escapes the uploads directory
//
// Validates: Requirements 3.1, 3.2, 3.3
func TestPropertyFileDeletionStaysInUploadsDir(t *testing.T) {
	prop := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		tr := mfNewTree(t)
		root := mfResolve(tr.uploads)
		mfWrite(t, filepath.Join(tr.uploads, "main.jpg"))
		if err := os.Symlink(tr.outside, filepath.Join(tr.uploads, "linkdir")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		mfWrite(t, filepath.Join(tr.uploads, "real", "seed.txt"))
		if err := os.Symlink(filepath.Join(tr.uploads, "real"), filepath.Join(tr.uploads, "alias")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}

		var shapes []mfShape
		var outsideFiles []string
		for i := 0; i < 1+rng.Intn(8); i++ {
			id := strconv.Itoa(i)
			switch rng.Intn(7) {
			case 0: // plain relative
				mfWrite(t, filepath.Join(tr.uploads, "p"+id+".jpg"))
				shapes = append(shapes, mfShape{"p" + id + ".jpg", true, filepath.Join(root, "p"+id+".jpg")})
			case 1: // .. that stays inside
				mfWrite(t, filepath.Join(tr.uploads, "q"+id+".jpg"))
				shapes = append(shapes, mfShape{"a/../q" + id + ".jpg", true, filepath.Join(root, "q"+id+".jpg")})
			case 2: // .. that escapes
				v := filepath.Join(tr.outside, "d"+id+".jpg")
				mfWrite(t, v)
				outsideFiles = append(outsideFiles, v)
				shapes = append(shapes, mfShape{cand: "../outside/d" + id + ".jpg"})
			case 3: // absolute, outside
				v := filepath.Join(tr.outside, "e"+id+".jpg")
				mfWrite(t, v)
				outsideFiles = append(outsideFiles, v)
				shapes = append(shapes, mfShape{cand: v})
			case 4: // symlinked directory escape
				v := filepath.Join(tr.outside, "f"+id+".jpg")
				mfWrite(t, v)
				outsideFiles = append(outsideFiles, v)
				shapes = append(shapes, mfShape{cand: "linkdir/f" + id + ".jpg"})
			case 5: // symlinked file escape
				v := filepath.Join(tr.outside, "g"+id+".jpg")
				mfWrite(t, v)
				outsideFiles = append(outsideFiles, v)
				if err := os.Symlink(v, filepath.Join(tr.uploads, "leaf"+id+".jpg")); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
				shapes = append(shapes, mfShape{cand: "leaf" + id + ".jpg"})
			default: // symlink that stays inside
				mfWrite(t, filepath.Join(tr.uploads, "real", "h"+id+".jpg"))
				shapes = append(shapes, mfShape{"alias/h" + id + ".jpg", true, filepath.Join(root, "real", "h"+id+".jpg")})
			}
		}
		cands := make([]string, len(shapes))
		for i, s := range shapes {
			cands[i] = s.cand
		}
		attempts := mfSeam(t, nil)

		deleteMediaFiles(tr.cfg(), "main.jpg", cands, nil)

		resolvedAttempts := map[string]bool{}
		for _, a := range *attempts {
			r := mfResolve(a)
			if !mfWithin(root, r) {
				t.Logf("seed %d: unlink attempted outside the root: %s", seed, a)
				return false
			}
			resolvedAttempts[r] = true
		}
		for _, o := range outsideFiles {
			if !mfExists(o) {
				t.Logf("seed %d: outside file deleted: %s", seed, o)
				return false
			}
		}
		for _, s := range shapes {
			if s.definiteIn && !resolvedAttempts[s.wantPath] {
				t.Logf("seed %d: in-bounds candidate %q not attempted (attempts %v)", seed, s.cand, *attempts)
				return false
			}
		}
		return resolvedAttempts[filepath.Join(root, "main.jpg")]
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(23))}); err != nil {
		t.Fatal(err)
	}
}

// ------------------- Property 4: failures never change the delete outcome

// TestPropertyFileDeletionFailuresAreNonFatal checks Property 4.
//
// Feature: media-delete-and-purge-config, Property 4: File deletion failures never change the delete outcome
//
// Validates: Requirements 4.2, 4.3, 4.4
func TestPropertyFileDeletionFailuresAreNonFatal(t *testing.T) {
	prop := func(seed int64) bool {
		rng := rand.New(rand.NewSource(seed))
		tr := mfNewTree(t)
		root := mfResolve(tr.uploads)
		mfWrite(t, filepath.Join(tr.uploads, "main.jpg"))

		n := rng.Intn(10)
		cands := make([]string, n)
		outcome := map[string]int{filepath.Join(root, "main.jpg"): rng.Intn(3)}
		for i := range cands {
			cands[i] = "c" + strconv.Itoa(i) + ".jpg"
			mfWrite(t, filepath.Join(tr.uploads, cands[i]))
			outcome[filepath.Join(root, cands[i])] = rng.Intn(3) // 0 ok, 1 missing, 2 failing
		}
		count := map[string]int{}
		mfSeam(t, func(p string) error {
			r := mfResolve(p)
			count[r]++
			switch outcome[r] {
			case 1:
				return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrNotExist}
			case 2:
				return &fs.PathError{Op: "remove", Path: p, Err: fs.ErrPermission}
			}
			return nil
		})
		var log *slog.Logger
		if rng.Intn(2) == 0 {
			log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError + 8}))
		}

		deleteMediaFiles(tr.cfg(), "main.jpg", cands, log) // must return normally, no error surface

		if len(count) != len(outcome) {
			t.Logf("seed %d: attempted %d distinct paths, want %d", seed, len(count), len(outcome))
			return false
		}
		for p, c := range count {
			if c != 1 {
				t.Logf("seed %d: %s attempted %d times", seed, p, c)
				return false
			}
		}
		return true
	}
	if err := quick.Check(prop, &quick.Config{MaxCount: 100, Rand: rand.New(rand.NewSource(23))}); err != nil {
		t.Fatal(err)
	}
}

// TestMediaFileCandidatesWithDouble covers real WordPress metadata that stores
// a dimension as a PHP double (`d:1600;`). Before php.Unserialize decoded
// doubles, the whole value failed to decode and no size file was deleted.
func TestMediaFileCandidatesWithDouble(t *testing.T) {
	meta := `a:4:{s:5:"width";i:1600;s:6:"height";d:1600;s:4:"file";s:13:"2024/02/a.jpg";s:5:"sizes";a:1:{s:9:"thumbnail";a:3:{s:4:"file";s:11:"a-150x1.jpg";s:5:"width";i:150;s:6:"height";d:150;}}}`
	got := mediaFileCandidates(meta, "")
	if len(got) != 1 || got[0] != "a-150x1.jpg" {
		t.Fatalf("mediaFileCandidates = %v, want [a-150x1.jpg]", got)
	}
}
