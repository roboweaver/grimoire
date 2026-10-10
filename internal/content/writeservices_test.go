package content

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// --- fakes for the writer ports ---------------------------------------------

type fakePostWriter struct {
	store            map[int64]domain.Post // persisted records, keyed by ID
	created, updated *domain.Post
	deleted          int64
	nextID           int64
	byIDErr          error
}

func (f *fakePostWriter) ByID(_ context.Context, id int64) (domain.Post, error) {
	if f.byIDErr != nil {
		return domain.Post{}, f.byIDErr
	}
	p, ok := f.store[id]
	if !ok {
		return domain.Post{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakePostWriter) Create(_ context.Context, p domain.Post) (int64, error) {
	cp := p
	f.created = &cp
	if f.nextID == 0 {
		f.nextID = 42
	}
	return f.nextID, nil
}
func (f *fakePostWriter) Update(_ context.Context, p domain.Post) error {
	cp := p
	f.updated = &cp
	return nil
}
func (f *fakePostWriter) Delete(_ context.Context, id int64) error {
	f.deleted = id
	return nil
}

// createRevisionCall records one CreateRevision invocation for assertions in
// RevisionWriteService/AutosaveService tests.
type createRevisionCall struct {
	parentID, authorID int64
	snapshot           domain.Post
	autosave           bool
}

// pruneRevisionsCall records one PruneRevisions invocation.
type pruneRevisionsCall struct {
	parentID int64
	keep     int
}

// fakeRevisionWriter is a full domain.RevisionWriter fake used across the
// PostWriteService.Delete cascade test (task 1.10) and the
// RevisionWriteService/AutosaveService tests (Phase 2). Every method records
// its call so tests can assert both "was called with X" and "was never
// called".
type fakeRevisionWriter struct {
	createRevisionCalls []createRevisionCall
	nextRevisionID      int64
	createRevisionErr   error

	listRevisions    []domain.RevisionMeta
	listRevisionsErr error

	revisionByID    domain.Post
	revisionByIDErr error

	autosavePost      domain.Post
	autosaveFound     bool
	autosaveForErr    error
	lastAutosaveForID struct{ parentID, authorID int64 }

	updateAutosaveErr  error
	lastUpdateAutosave struct {
		revisionID int64
		snapshot   domain.Post
	}

	pruneRevisionsCalls []pruneRevisionsCall
	pruneRevisionsErr   error

	deletedRevisionsOf   int64
	deleteRevisionsOfErr error
}

func (f *fakeRevisionWriter) CreateRevision(_ context.Context, parentID, authorID int64, snapshot domain.Post, autosave bool) (int64, error) {
	f.createRevisionCalls = append(f.createRevisionCalls, createRevisionCall{parentID, authorID, snapshot, autosave})
	if f.createRevisionErr != nil {
		return 0, f.createRevisionErr
	}
	return f.nextRevisionID, nil
}
func (f *fakeRevisionWriter) ListRevisions(_ context.Context, _ int64) ([]domain.RevisionMeta, error) {
	return f.listRevisions, f.listRevisionsErr
}
func (f *fakeRevisionWriter) RevisionByID(_ context.Context, _ int64) (domain.Post, error) {
	return f.revisionByID, f.revisionByIDErr
}
func (f *fakeRevisionWriter) AutosaveFor(_ context.Context, parentID, authorID int64) (domain.Post, bool, error) {
	f.lastAutosaveForID.parentID = parentID
	f.lastAutosaveForID.authorID = authorID
	return f.autosavePost, f.autosaveFound, f.autosaveForErr
}
func (f *fakeRevisionWriter) UpdateAutosave(_ context.Context, revisionID int64, snapshot domain.Post) error {
	f.lastUpdateAutosave.revisionID = revisionID
	f.lastUpdateAutosave.snapshot = snapshot
	return f.updateAutosaveErr
}
func (f *fakeRevisionWriter) PruneRevisions(_ context.Context, parentID int64, keep int) error {
	f.pruneRevisionsCalls = append(f.pruneRevisionsCalls, pruneRevisionsCall{parentID, keep})
	return f.pruneRevisionsErr
}
func (f *fakeRevisionWriter) DeleteRevisionsOf(_ context.Context, parentID int64) error {
	f.deletedRevisionsOf = parentID
	return f.deleteRevisionsOfErr
}

// postWriterWithRevisions combines a fakePostWriter and a fakeRevisionWriter
// into a single value satisfying both domain.PostWriter and
// domain.RevisionWriter, mirroring how wprepo.PostRepo backs both ports with
// one concrete type in production.
type postWriterWithRevisions struct {
	*fakePostWriter
	*fakeRevisionWriter
}

type fakeTermWriter struct {
	created *domain.Term
	updated *domain.Term
	deleted int64
	// byTaxonomy/byID back the TermReader half of the combined interface
	// NewTermWriteService now requires; ListByTaxonomy/TermsByIDs args are
	// captured so tests can assert the pass-through forwards them unchanged.
	byTaxonomy      map[string][]domain.Term
	byIDs           map[int64]domain.Term
	lastTaxonomyArg string
	lastIDsArg      []int64
}

func (f *fakeTermWriter) Create(_ context.Context, t domain.Term) (int64, error) {
	ct := t
	f.created = &ct
	return 7, nil
}
func (f *fakeTermWriter) Update(_ context.Context, t domain.Term) error {
	ct := t
	f.updated = &ct
	return nil
}
func (f *fakeTermWriter) Delete(_ context.Context, id int64) error {
	f.deleted = id
	return nil
}
func (f *fakeTermWriter) ListByTaxonomy(_ context.Context, taxonomy string) ([]domain.Term, error) {
	f.lastTaxonomyArg = taxonomy
	return f.byTaxonomy[taxonomy], nil
}
func (f *fakeTermWriter) TermsByIDs(_ context.Context, ids []int64) ([]domain.Term, error) {
	f.lastIDsArg = ids
	var out []domain.Term
	for _, id := range ids {
		if t, ok := f.byIDs[id]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

type fakeOptionWriter struct {
	set     map[string]string
	deleted string
}

func (f *fakeOptionWriter) Set(_ context.Context, name, value string) error {
	if f.set == nil {
		f.set = map[string]string{}
	}
	f.set[name] = value
	return nil
}
func (f *fakeOptionWriter) Delete(_ context.Context, name string) error {
	f.deleted = name
	return nil
}

// --- helpers ----------------------------------------------------------------

func actor(role string, id int64) auth.Principal {
	return auth.NewPrincipal(id, "u", []string{role})
}

// --- PostWriteService -------------------------------------------------------

func TestPostWriteCreateAllowedDefaultsAuthorAndType(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	id, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5),
		domain.Post{Title: "Hi", Status: "publish"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != 42 {
		t.Errorf("id = %d, want 42", id)
	}
	if w.created == nil {
		t.Fatal("writer not called")
	}
	if w.created.Author != 5 {
		t.Errorf("author defaulted to %d, want 5", w.created.Author)
	}
	if w.created.Type != "post" {
		t.Errorf("type defaulted to %q, want post", w.created.Type)
	}
}

// TestPostWriteCreateDefaultsDateWhenOmitted guards finding #1 from the PR
// #16 review: a caller (the admin API's PostEditor, notably) that omits
// date entirely must not get a post silently stored with a zero/epoch
// Date -- Create must default it to "now", matching WordPress's own
// new-post behavior, so posts sort correctly by post_date DESC.
func TestPostWriteCreateDefaultsDateWhenOmitted(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	before := time.Now()
	_, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5),
		domain.Post{Title: "Hi", Status: "draft"})
	after := time.Now()
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created.Date.Before(before) || w.created.Date.After(after) {
		t.Fatalf("created.Date = %v, want between %v and %v", w.created.Date, before, after)
	}
}

// TestPostWriteCreatePreservesExplicitDate guards the flip side: when the
// caller does supply a date (e.g. a future-status scheduled post, or an
// explicit backdated import), Create must not clobber it with "now".
func TestPostWriteCreatePreservesExplicitDate(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	explicit := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	_, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5),
		domain.Post{Title: "Hi", Status: "future", Date: explicit})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !w.created.Date.Equal(explicit) {
		t.Fatalf("created.Date = %v, want unchanged %v", w.created.Date, explicit)
	}
}

func TestPostWriteCreateDeniedReturnsForbiddenAndSkipsWriter(t *testing.T) {
	w := &fakePostWriter{}
	svc := NewPostWriteService(w)
	// Contributor cannot publish.
	_, err := svc.Create(context.Background(), actor(auth.RoleContributor, 5),
		domain.Post{Title: "Hi", Status: "publish"})
	if err != ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
	if w.created != nil {
		t.Error("writer must not be called on denial")
	}
}

func TestPostWriteUpdateAndDeleteEnforceOwnership(t *testing.T) {
	// The authoritative record lives in the store; the service must authorize
	// against it, not against the caller-supplied struct.
	seed := func() *fakePostWriter {
		return &fakePostWriter{store: map[int64]domain.Post{
			3: {ID: 3, Author: 999, Type: "post", Status: "publish"},
		}}
	}

	// Author cannot edit/delete someone else's published post, even though the
	// input struct is otherwise valid.
	w := seed()
	svc := NewPostWriteService(w)
	input := domain.Post{ID: 3, Author: 999, Type: "post", Status: "publish"}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), input, time.Time{}); err != ErrForbidden {
		t.Errorf("author update others: err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleAuthor, 5), input); err != ErrForbidden {
		t.Errorf("author delete others: err = %v, want ErrForbidden", err)
	}
	if w.updated != nil || w.deleted != 0 {
		t.Error("writer must not be called on denial")
	}

	// Editor can.
	w = seed()
	svc = NewPostWriteService(w)
	if err := svc.Update(context.Background(), actor(auth.RoleEditor, 5), input, time.Time{}); err != nil {
		t.Errorf("editor update: %v", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleEditor, 5), input); err != nil {
		t.Errorf("editor delete: %v", err)
	}
	if w.deleted != 3 {
		t.Errorf("deleted id = %d, want 3", w.deleted)
	}
}

// TestPostWriteDeleteCascadesToDeleteRevisionsOf is task 1.10's failing test
// (Req 1.6): deleting a post whose writer also implements domain.RevisionWriter
// must cascade into DeleteRevisionsOf for that post's ID, so no revision or
// autosave row is left behind pointing at a deleted parent. The underlying
// DeleteRevisionsOf query behavior itself (removing every revision, including
// the autosave) is already covered by the storagetest contract suite (task
// 1.7); this test only asserts PostWriteService.Delete triggers the cascade.
func TestPostWriteDeleteCascadesToDeleteRevisionsOf(t *testing.T) {
	w := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Author: 5, Type: "post", Status: "publish"},
	}}
	rw := &fakeRevisionWriter{}
	combined := postWriterWithRevisions{fakePostWriter: w, fakeRevisionWriter: rw}
	svc := NewPostWriteService(combined)

	if err := svc.Delete(context.Background(), actor(auth.RoleEditor, 5), domain.Post{ID: 9}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if w.deleted != 9 {
		t.Errorf("post deleted id = %d, want 9", w.deleted)
	}
	if rw.deletedRevisionsOf != 9 {
		t.Errorf("DeleteRevisionsOf called with parentID = %d, want 9", rw.deletedRevisionsOf)
	}
}

func TestPostWriteDeleteKeepsParentWhenRevisionCleanupFails(t *testing.T) {
	cleanupErr := errors.New("revision cleanup failed")
	w := &fakePostWriter{store: map[int64]domain.Post{
		9: {ID: 9, Author: 5, Type: "post", Status: "publish"},
	}}
	rw := &fakeRevisionWriter{deleteRevisionsOfErr: cleanupErr}
	combined := postWriterWithRevisions{fakePostWriter: w, fakeRevisionWriter: rw}
	svc := NewPostWriteService(combined)

	err := svc.Delete(context.Background(), actor(auth.RoleEditor, 5), domain.Post{ID: 9})

	if !errors.Is(err, cleanupErr) {
		t.Fatalf("delete error = %v, want %v", err, cleanupErr)
	}
	if w.deleted != 0 {
		t.Fatalf("post deleted id = %d after revision cleanup failure, want parent preserved", w.deleted)
	}
}

// TestPostWriteAuthorizesAgainstPersistedRecord is the regression for the
// forged-field privilege escalation: an author who owns post A must not be able
// to edit or delete post B (owned by someone else) by submitting a struct that
// claims {ID: B.ID, Author: self}. Authorization must use the STORED record.
func TestPostWriteAuthorizesAgainstPersistedRecord(t *testing.T) {
	const self = 5
	// Post B is owned by user 999 and published; the attacker owns nothing here.
	seed := func() *fakePostWriter {
		return &fakePostWriter{store: map[int64]domain.Post{
			20: {ID: 20, Author: 999, Type: "post", Status: "publish"},
		}}
	}

	// Forged input: attacker claims authorship of B to slip past a naive check.
	forged := domain.Post{ID: 20, Author: self, Type: "post", Status: "draft", Title: "pwned"}

	w := seed()
	svc := NewPostWriteService(w)
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), forged, time.Time{}); err != ErrForbidden {
		t.Errorf("forged update: err = %v, want ErrForbidden", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called when authz denies (persisted record)")
	}

	w = seed()
	svc = NewPostWriteService(w)
	if err := svc.Delete(context.Background(), actor(auth.RoleAuthor, self), forged); err != ErrForbidden {
		t.Errorf("forged delete: err = %v, want ErrForbidden", err)
	}
	if w.deleted != 0 {
		t.Error("writer.Delete must not be called when authz denies (persisted record)")
	}
}

// TestPostWriteUpdateOwnerAppliesMutableFields confirms the owner can edit their
// own post and that mutable content fields are applied to the persisted record
// while identity/ownership (ID, Author, Type) come from the store, not input.
func TestPostWriteUpdateOwnerAppliesMutableFields(t *testing.T) {
	const self = 5
	w := &fakePostWriter{store: map[int64]domain.Post{
		7: {ID: 7, Author: self, Type: "post", Status: "draft", Title: "old"},
	}}
	svc := NewPostWriteService(w)
	// Caller tries to reassign the author; that field must be ignored.
	in := domain.Post{ID: 7, Author: 999, Title: "new title", Content: "body"}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("owner update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if w.updated.Author != self {
		t.Errorf("author = %d, want %d (must come from persisted record)", w.updated.Author, self)
	}
	if w.updated.Title != "new title" || w.updated.Content != "body" {
		t.Errorf("mutable fields not applied: %+v", *w.updated)
	}
}

// TestPostWriteUpdateAppliesCommentStatus guards against a regression where
// CommentStatus was read into the stored record but never merged from the
// caller's input before writing (Req 1.2's commentStatus field would then be
// silently dropped on every admin-API update).
func TestPostWriteUpdateAppliesCommentStatus(t *testing.T) {
	const self = 5
	w := &fakePostWriter{store: map[int64]domain.Post{
		7: {ID: 7, Author: self, Type: "post", Status: "draft", CommentStatus: "open"},
	}}
	svc := NewPostWriteService(w)
	in := domain.Post{ID: 7, Author: self, CommentStatus: "closed"}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if w.updated.CommentStatus != "closed" {
		t.Errorf("CommentStatus = %q, want %q", w.updated.CommentStatus, "closed")
	}
}

// TestPostWriteUpdateChangesDateGMT guards against a regression where
// PostWriteService.Update applied a new Date to the loaded record without
// also clearing/re-deriving DateGMT. PostRepo.Update only re-derives
// post_date_gmt from Date when the incoming DateGMT is zero, but cur is
// loaded via ByID here — which always returns a non-zero DateGMT for any
// real stored post — so unless the service itself clears DateGMT when Date
// actually changes, the repo's derivation branch is unreachable in
// production and post_date_gmt would stay stuck at its original value
// forever across date changes (PR #16 finding: date_gmt update-path dead
// code). This exercises the real service call path end-to-end, not a
// hand-zeroed repo call.
func TestPostWriteUpdateChangesDateGMT(t *testing.T) {
	const self = 5
	oldDate := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	oldDateGMT := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	w := &fakePostWriter{store: map[int64]domain.Post{
		7: {ID: 7, Author: self, Type: "post", Status: "draft", Date: oldDate, DateGMT: oldDateGMT},
	}}
	svc := NewPostWriteService(w)
	newDate := time.Date(2023, 3, 15, 12, 0, 0, 0, time.UTC)
	in := domain.Post{ID: 7, Author: self, Date: newDate}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if !w.updated.Date.Equal(newDate) {
		t.Errorf("Date = %v, want %v", w.updated.Date, newDate)
	}
	if !w.updated.DateGMT.IsZero() {
		t.Errorf("DateGMT = %v, want zero (so PostRepo.Update re-derives it from the new Date)", w.updated.DateGMT)
	}
}

// TestPostWriteUpdateUnchangedDatePreservesDateGMT is the flip side of
// TestPostWriteUpdateChangesDateGMT: when the caller resubmits the same Date
// (or omits it), DateGMT must be passed through unchanged rather than
// cleared, so an update to an unrelated field never wipes a correct,
// already-consistent post_date_gmt.
func TestPostWriteUpdateUnchangedDatePreservesDateGMT(t *testing.T) {
	const self = 5
	date := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	dateGMT := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	w := &fakePostWriter{store: map[int64]domain.Post{
		7: {ID: 7, Author: self, Type: "post", Status: "draft", Date: date, DateGMT: dateGMT, Title: "old"},
	}}
	svc := NewPostWriteService(w)
	in := domain.Post{ID: 7, Author: self, Date: date, Title: "new title"}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if !w.updated.DateGMT.Equal(dateGMT) {
		t.Errorf("DateGMT = %v, want unchanged %v", w.updated.DateGMT, dateGMT)
	}
}

// TestPostWriteUpdateNotFoundIsForbidden ensures a missing record does not leak
// existence: the service returns the generic ErrForbidden and never writes.
func TestPostWriteUpdateNotFoundIsForbidden(t *testing.T) {
	w := &fakePostWriter{store: map[int64]domain.Post{}}
	svc := NewPostWriteService(w)
	in := domain.Post{ID: 404, Author: 5, Type: "post"}
	if err := svc.Update(context.Background(), actor(auth.RoleAdministrator, 5), in, time.Time{}); err != ErrForbidden {
		t.Errorf("update missing: err = %v, want ErrForbidden", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 5), in); err != ErrForbidden {
		t.Errorf("delete missing: err = %v, want ErrForbidden", err)
	}
	if w.updated != nil || w.deleted != 0 {
		t.Error("writer must not be called for a missing record")
	}
}

// TestPostWriteUpdateConflictDetection covers design.md's
// "authorize-then-compare" optimistic-concurrency sequence: an unauthorized
// caller must see ErrForbidden even when expectedModified is stale (never a
// *ConflictError leaking cur.Modified), a matching expectedModified proceeds
// normally, a mismatched one returns *ConflictError carrying the current
// value without calling the writer, and a zero expectedModified is the
// skip-the-check escape hatch.
func TestPostWriteUpdateConflictDetection(t *testing.T) {
	stored := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	seed := func() *fakePostWriter {
		return &fakePostWriter{store: map[int64]domain.Post{
			9: {ID: 9, Author: 5, Type: "post", Status: "draft", Modified: stored},
		}}
	}
	in := domain.Post{ID: 9, Title: "new"}

	// Unauthorized + stale expectedModified: must be ErrForbidden, not a
	// ConflictError — an unauthorized caller must never learn cur.Modified.
	w := seed()
	svc := NewPostWriteService(w)
	stale := stored.Add(-time.Hour)
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 999), in, stale); err != ErrForbidden {
		t.Errorf("unauthorized+stale: err = %v, want ErrForbidden (not ConflictError)", err)
	}

	// Authorized + matching expectedModified: proceeds.
	w = seed()
	svc = NewPostWriteService(w)
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), in, stored); err != nil {
		t.Errorf("authorized+matching: %v", err)
	}
	if w.updated == nil {
		t.Error("writer.Update not called on matching expectedModified")
	}

	// Authorized + mismatched expectedModified: *ConflictError, no write.
	w = seed()
	svc = NewPostWriteService(w)
	err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), in, stale)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("authorized+mismatched: err = %v, want *ConflictError", err)
	}
	if !conflict.CurrentModified.Equal(stored) {
		t.Errorf("CurrentModified = %v, want %v", conflict.CurrentModified, stored)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on conflict")
	}

	// Authorized + zero expectedModified: check skipped, proceeds.
	w = seed()
	svc = NewPostWriteService(w)
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), in, time.Time{}); err != nil {
		t.Errorf("authorized+zero expectedModified: %v", err)
	}
	if w.updated == nil {
		t.Error("writer.Update not called when expectedModified is zero")
	}
}

// --- TermWriteService -------------------------------------------------------

func TestTermWriteAuthz(t *testing.T) {
	w := &fakeTermWriter{}
	svc := NewTermWriteService(w)
	term := domain.Term{Name: "News", Slug: "news", Taxonomy: "category"}

	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 1), term); err != ErrForbidden {
		t.Errorf("author create term: err = %v, want ErrForbidden", err)
	}
	if w.created != nil {
		t.Error("writer called on denial")
	}
	if _, err := svc.Create(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
		t.Errorf("editor create term: %v", err)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleEditor, 1), 7); err != nil {
		t.Errorf("editor delete term: %v", err)
	}
	if w.deleted != 7 {
		t.Errorf("deleted = %d, want 7", w.deleted)
	}
}

// TestTermWriteUpdateAuthz mirrors TestTermWriteAuthz's Create/Delete coverage
// for the new Update method: same manage_categories gate, same writer-not-
// called-on-denial guarantee.
func TestTermWriteUpdateAuthz(t *testing.T) {
	w := &fakeTermWriter{}
	svc := NewTermWriteService(w)
	term := domain.Term{ID: 7, Name: "Renamed", Slug: "renamed"}

	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 1), term); err != ErrForbidden {
		t.Errorf("author update term: err = %v, want ErrForbidden", err)
	}
	if w.updated != nil {
		t.Error("writer called on denial")
	}
	if err := svc.Update(context.Background(), actor(auth.RoleEditor, 1), term); err != nil {
		t.Errorf("editor update term: %v", err)
	}
	if w.updated == nil || w.updated.Name != "Renamed" {
		t.Errorf("updated = %+v, want Name=Renamed", w.updated)
	}
}

// TestTermWriteReadPassthroughsAreUnauthorized confirms ListByTaxonomy and
// TermsByIDs perform no capability check of their own (design.md: "these
// require only edit_posts, not manage_categories" — enforced by the web
// layer's route middleware, not the service) and simply forward to the
// underlying TermReader, matching AdminService's established
// read-only-service convention (no actor parameter at all).
func TestTermWriteReadPassthroughsAreUnauthorized(t *testing.T) {
	w := &fakeTermWriter{
		byTaxonomy: map[string][]domain.Term{
			"category": {{ID: 1, Name: "News", Slug: "news", Taxonomy: "category"}},
		},
		byIDs: map[int64]domain.Term{
			1: {ID: 1, Name: "News", Slug: "news", Taxonomy: "category"},
		},
	}
	svc := NewTermWriteService(w)

	got, err := svc.ListByTaxonomy(context.Background(), "category")
	if err != nil {
		t.Fatalf("ListByTaxonomy: %v", err)
	}
	if len(got) != 1 || got[0].Name != "News" {
		t.Errorf("ListByTaxonomy = %+v, want [News]", got)
	}
	if w.lastTaxonomyArg != "category" {
		t.Errorf("taxonomy arg not forwarded: got %q", w.lastTaxonomyArg)
	}

	got2, err := svc.TermsByIDs(context.Background(), []int64{1, 999})
	if err != nil {
		t.Fatalf("TermsByIDs: %v", err)
	}
	if len(got2) != 1 || got2[0].ID != 1 {
		t.Errorf("TermsByIDs = %+v, want [{ID:1}]", got2)
	}
}

// --- PostTermsWriteService ---------------------------------------------------

type fakePostTermsWriter struct {
	postID   int64
	taxonomy string
	termIDs  []int64
	called   bool
}

func (f *fakePostTermsWriter) SetPostTerms(_ context.Context, postID int64, taxonomy string, termIDs []int64) error {
	f.called = true
	f.postID = postID
	f.taxonomy = taxonomy
	f.termIDs = termIDs
	return nil
}

// TestPostTermsWriteAuthorizesAgainstPersistedPost confirms SetPostTerms is
// authorized as an edit of the TARGET POST (auth.CanEditPost against its
// stored record, loaded by ID) rather than the separate manage_categories
// capability that Term Create/Update/Delete require — design.md: "assigning
// terms is part of editing the post, not a separate manage_categories
// action." An author may set terms on their own draft but not on another
// user's published post; a missing post is the generic ErrForbidden.
func TestPostTermsWriteAuthorizesAgainstPersistedPost(t *testing.T) {
	posts := &fakePostWriter{store: map[int64]domain.Post{
		3: {ID: 3, Author: 5, Type: "post", Status: "draft"},
		4: {ID: 4, Author: 999, Type: "post", Status: "publish"},
	}}
	w := &fakePostTermsWriter{}
	svc := NewPostTermsWriteService(posts, w)

	// Owner (author of post 3, which is a draft they may edit).
	if err := svc.SetPostTerms(context.Background(), actor(auth.RoleAuthor, 5), 3, "category", []int64{1, 2}); err != nil {
		t.Errorf("owner set terms: %v", err)
	}
	if !w.called || w.postID != 3 || w.taxonomy != "category" || len(w.termIDs) != 2 {
		t.Errorf("writer not called as expected: %+v", w)
	}

	// Same author cannot set terms on someone else's published post.
	w = &fakePostTermsWriter{}
	svc = NewPostTermsWriteService(posts, w)
	if err := svc.SetPostTerms(context.Background(), actor(auth.RoleAuthor, 5), 4, "category", []int64{1}); err != ErrForbidden {
		t.Errorf("non-owner set terms: err = %v, want ErrForbidden", err)
	}
	if w.called {
		t.Error("writer must not be called on denial")
	}

	// Missing post: generic ErrForbidden, existence not leaked.
	if err := svc.SetPostTerms(context.Background(), actor(auth.RoleAdministrator, 1), 404, "category", nil); err != ErrForbidden {
		t.Errorf("missing post: err = %v, want ErrForbidden", err)
	}
}

// --- OptionWriteService -----------------------------------------------------

func TestOptionWriteAuthz(t *testing.T) {
	w := &fakeOptionWriter{}
	svc := NewOptionWriteService(w)

	if err := svc.Set(context.Background(), actor(auth.RoleEditor, 1), "blogname", "X"); err != ErrForbidden {
		t.Errorf("editor set option: err = %v, want ErrForbidden", err)
	}
	if len(w.set) != 0 {
		t.Error("writer called on denial")
	}
	if err := svc.Set(context.Background(), actor(auth.RoleAdministrator, 1), "blogname", "X"); err != nil {
		t.Errorf("admin set option: %v", err)
	}
	if w.set["blogname"] != "X" {
		t.Errorf("option not written: %v", w.set)
	}
	if err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), "blogname"); err != nil {
		t.Errorf("admin delete option: %v", err)
	}
	if w.deleted != "blogname" {
		t.Errorf("deleted = %q, want blogname", w.deleted)
	}
}

// --- M10a: PostWriteService content-safety wiring (tasks 5.1, 5.2) ----------
//
// These tests pin the post-write sanitization wiring described in design.md's
// "Write-path wiring (Requirement 4)" and the "Ordering in
// PostWriteService.Update" sequence. They are written failing-first (strict
// TDD): they reference symbols that task 5.3 adds and do not yet exist, so
// `go test ./internal/content` fails to COMPILE until the implementation lands.
//
// The assumed shapes task 5.3 must match (chosen to agree with design.md and
// tasks.md 5.1/5.2/5.3, not invented here):
//
//   - A functional option `WithContentPolicy(*sanitize.Policy) PostWriteOption`,
//     following the existing WithRevisionSnapshotter option style, setting a new
//     `policy *sanitize.Policy` field on PostWriteService. Per design the field
//     defaults to sanitize.New() (never nil / fail closed) when the option is
//     omitted; these tests always pass WithContentPolicy(sanitize.New()) so they
//     exercise a real, production-shaped policy regardless of that default.
//   - An `Update` helper `sanitizeIncoming(actor auth.Principal, p, cur
//     domain.Post) (domain.Post, error)` invoked AFTER the revision Snapshot and
//     BEFORE the field merge, with the Finding-3 byte-identity skip.
//   - A sentinel `var ErrTitleEmpty` returned by Create/Update when post_title
//     sanitizes to empty (design "Emptied fields": "<em></em> sanitizes to
//     empty, and both transports require a non-empty title").
//
// Actors: an author (RoleAuthor) lacks unfiltered_html, so TierFor selects tier
// B for content/excerpt and the title plain-text path. An editor (RoleEditor)
// holds unfiltered_html, so TierFor selects tier C (byte-identical passthrough).

// policyWriteSvc builds a PostWriteService wired with a real sanitize.Policy via
// the WithContentPolicy option (plus any extra options). Centralised so the
// assumed option name lives in one place for task 5.3 to match.
func policyWriteSvc(w domain.PostWriter, extra ...PostWriteOption) *PostWriteService {
	opts := append([]PostWriteOption{WithContentPolicy(sanitize.New())}, extra...)
	return NewPostWriteService(w, opts...)
}

// recordingSnapshotter is a minimal revisionSnapshotter test double for the
// M10a Update-ordering tests: it records each snapshotted cur by value so a test
// can assert the snapshot held the UNMUTATED pre-edit stored row (Req 9.7), and
// counts calls so a test can assert the snapshot did NOT run (conflict/authz
// paths return before it). It is intentionally narrower than fakeRevisionWriter,
// which records via CreateRevision rather than the Snapshot port Update uses.
type recordingSnapshotter struct {
	snapshots []domain.Post
}

func (r *recordingSnapshotter) Snapshot(_ context.Context, cur domain.Post, _ int64) error {
	r.snapshots = append(r.snapshots, cur)
	return nil
}

// TestPostWriteCreateSanitizesContentAndExcerptAtTierB (task 5.1, Req 4.2, 9.4,
// 9.6): for a writer lacking unfiltered_html (author -> tier B), Create strips a
// disallowed <script> from post_content and post_excerpt while an allow-listed
// <em> survives, with assertions made on what reached the fake PostWriter.
func TestPostWriteCreateSanitizesContentAndExcerptAtTierB(t *testing.T) {
	w := &fakePostWriter{}
	svc := policyWriteSvc(w)
	in := domain.Post{
		Title:   "Clean Title",
		Content: `<em>ok</em><script>alert(1)</script>`,
		Excerpt: `<em>ex</em><script>alert(2)</script>`,
		Status:  "publish",
	}
	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5), in); err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created == nil {
		t.Fatal("writer not called")
	}
	if strings.Contains(w.created.Content, "<script") {
		t.Errorf("content retained <script>: %q", w.created.Content)
	}
	if !strings.Contains(w.created.Content, "<em>") {
		t.Errorf("content dropped allow-listed <em>: %q", w.created.Content)
	}
	if strings.Contains(w.created.Excerpt, "<script") {
		t.Errorf("excerpt retained <script>: %q", w.created.Excerpt)
	}
	if !strings.Contains(w.created.Excerpt, "<em>") {
		t.Errorf("excerpt dropped allow-listed <em>: %q", w.created.Excerpt)
	}
}

// TestPostWriteCreateReducesTitleToPlainText (task 5.1, Req 4.2): Create reduces
// post_title to plain text for a tier-B writer -- no markup delimiters and no
// surviving tag survive to the writer.
func TestPostWriteCreateReducesTitleToPlainText(t *testing.T) {
	w := &fakePostWriter{}
	svc := policyWriteSvc(w)
	in := domain.Post{Title: `<em>Hello</em>`, Content: "body", Status: "publish"}
	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5), in); err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created == nil {
		t.Fatal("writer not called")
	}
	if got := w.created.Title; got != "Hello" {
		t.Errorf("title = %q, want plain text %q", got, "Hello")
	}
}

// TestPostWriteCreateEmptyTitleReturnsErrTitleEmpty (task 5.1, Req 4.2): a title
// that sanitizes to empty (markup-only, e.g. "<em></em>") makes Create return
// the new ErrTitleEmpty sentinel and never call the writer -- the required
// non-empty-title rule evaluated against the sanitized value (design "Emptied
// fields").
func TestPostWriteCreateEmptyTitleReturnsErrTitleEmpty(t *testing.T) {
	w := &fakePostWriter{}
	svc := policyWriteSvc(w)
	in := domain.Post{Title: `<em></em>`, Content: "body", Status: "publish"}
	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 5), in); err != ErrTitleEmpty {
		t.Fatalf("create empty title: err = %v, want ErrTitleEmpty", err)
	}
	if w.created != nil {
		t.Error("writer must not be called when the title sanitizes to empty")
	}
}

// TestPostWriteCreateTierCPersistsByteIdentical (task 5.1, Req 4.2, 9.6): an
// editor/administrator holds unfiltered_html, so tier C persists the input
// byte-identically -- even input tier B would demonstrably alter (<script> and a
// raw markup title). Nothing is stripped and the title keeps its markup.
func TestPostWriteCreateTierCPersistsByteIdentical(t *testing.T) {
	w := &fakePostWriter{}
	svc := policyWriteSvc(w)
	content := `<em>ok</em><script>alert(1)</script>`
	title := `<em>Keep Me</em>`
	in := domain.Post{Title: title, Content: content, Excerpt: content, Status: "publish"}
	if _, err := svc.Create(context.Background(), actor(auth.RoleEditor, 5), in); err != nil {
		t.Fatalf("create: %v", err)
	}
	if w.created == nil {
		t.Fatal("writer not called")
	}
	if w.created.Content != content {
		t.Errorf("content = %q, want byte-identical %q", w.created.Content, content)
	}
	if w.created.Excerpt != content {
		t.Errorf("excerpt = %q, want byte-identical %q", w.created.Excerpt, content)
	}
	if w.created.Title != title {
		t.Errorf("title = %q, want byte-identical %q", w.created.Title, title)
	}
}

// TestPostWriteCreateDeniedSkipsSanitization (task 5.1, Req 4.2): an unauthorized
// caller never has their input sanitized or written -- Create returns ErrForbidden
// from the auth gate before the Policy is touched. Guards the design ordering
// "sanitize immediately after auth.CanCreatePost", not before it.
func TestPostWriteCreateDeniedSkipsSanitization(t *testing.T) {
	w := &fakePostWriter{}
	svc := policyWriteSvc(w)
	// Contributor cannot publish; the gate rejects before any write.
	in := domain.Post{Title: `<em></em>`, Content: `<script>x</script>`, Status: "publish"}
	if err := func() error {
		_, err := svc.Create(context.Background(), actor(auth.RoleContributor, 5), in)
		return err
	}(); err != ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden (not ErrTitleEmpty or a sanitize error)", err)
	}
	if w.created != nil {
		t.Error("writer must not be called on denial")
	}
}

// TestPostWriteUpdateSnapshotHoldsPreEditStoredValue (task 5.2, Req 4.3, 9.7):
// the revision snapshot must receive the UNMUTATED stored row (historical
// value), while the post-update row holds the sanitized new value. Pins the
// design sequence: Snapshot(cur) runs BEFORE sanitizeIncoming mutates cur.
func TestPostWriteUpdateSnapshotHoldsPreEditStoredValue(t *testing.T) {
	const self = 5
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old title", Content: "old body"}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	snaps := &recordingSnapshotter{}
	svc := policyWriteSvc(w, WithRevisionSnapshotter(snaps))

	in := domain.Post{ID: 7, Author: self, Title: "new title",
		Content: `<em>new</em><script>evil</script>`}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	// Snapshot holds the pre-edit stored value, unmutated.
	if len(snaps.snapshots) != 1 {
		t.Fatalf("snapshot calls = %d, want 1", len(snaps.snapshots))
	}
	snap := snaps.snapshots[0]
	if snap.Title != "old title" || snap.Content != "old body" {
		t.Errorf("snapshot = {Title:%q Content:%q}, want the pre-edit stored value", snap.Title, snap.Content)
	}
	// The post-update row holds the sanitized new value.
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if strings.Contains(w.updated.Content, "<script") {
		t.Errorf("stored content retained <script>: %q", w.updated.Content)
	}
	if !strings.Contains(w.updated.Content, "<em>") {
		t.Errorf("stored content dropped allow-listed <em>: %q", w.updated.Content)
	}
}

// TestPostWriteUpdateUnchangedFieldPassesThroughUnsanitized (task 5.2, Req 9.14,
// Finding 3): a sparse update whose content is byte-identical to the stored
// value must be passed through UNSANITIZED -- re-sanitizing a merged base value
// would rewrite pre-M10a/imported content, which Requirement 5.2 forbids. So a
// title-only edit leaves a stored <script> in content byte-identical, while a
// genuinely changed field IS sanitized.
func TestPostWriteUpdateUnchangedFieldPassesThroughUnsanitized(t *testing.T) {
	const self = 5
	// The stored content contains markup tier B WOULD strip; it predates M10a.
	storedContent := `<script>legacy()</script>`
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old title", Content: storedContent}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	svc := policyWriteSvc(w)

	// REST partial update: the merge base means Content arrives == cur.Content.
	in := domain.Post{ID: 7, Author: self, Title: "edited title", Content: storedContent}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if w.updated.Content != storedContent {
		t.Errorf("unchanged content was re-sanitized: got %q, want byte-identical %q (Finding 3)", w.updated.Content, storedContent)
	}
	if w.updated.Title != "edited title" {
		t.Errorf("title = %q, want %q", w.updated.Title, "edited title")
	}
}

// TestPostWriteUpdateChangedFieldIsSanitized (task 5.2, Finding 3 flip side): a
// content value that DIFFERS from the stored value is a genuine caller value and
// IS sanitized at the actor's tier.
func TestPostWriteUpdateChangedFieldIsSanitized(t *testing.T) {
	const self = 5
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old title", Content: "old body"}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	svc := policyWriteSvc(w)

	in := domain.Post{ID: 7, Author: self, Title: "old title",
		Content: `<em>new</em><script>evil</script>`}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if strings.Contains(w.updated.Content, "<script") {
		t.Errorf("changed content retained <script>: %q", w.updated.Content)
	}
	if !strings.Contains(w.updated.Content, "<em>") {
		t.Errorf("changed content dropped allow-listed <em>: %q", w.updated.Content)
	}
}

// TestPostWriteUpdateSanitizeNotRunOnAuthFailure (task 5.2, Req 4.3): an
// unauthorized caller never has input sanitized -- Update returns ErrForbidden,
// not ErrTitleEmpty, even when the forged title would sanitize to empty.
func TestPostWriteUpdateSanitizeNotRunOnAuthFailure(t *testing.T) {
	stored := domain.Post{ID: 7, Author: 999, Type: "post", Status: "publish",
		Title: "owned title", Content: "owned body"}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	svc := policyWriteSvc(w)

	// Author (id 5) does not own post 7; the title would sanitize to empty.
	in := domain.Post{ID: 7, Author: 5, Title: `<em></em>`, Content: `<script>x</script>`}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, 5), in, time.Time{}); err != ErrForbidden {
		t.Fatalf("err = %v, want ErrForbidden (sanitize must not run before authz)", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on denial")
	}
}

// TestPostWriteUpdateSanitizeNotRunOnConflict (task 5.2, Req 4.3): the
// optimistic-concurrency conflict returns BEFORE sanitize/snapshot run -- a
// conflicting update whose title would sanitize to empty returns *ConflictError,
// not ErrTitleEmpty, and takes no snapshot.
func TestPostWriteUpdateSanitizeNotRunOnConflict(t *testing.T) {
	const self = 5
	storedMod := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old", Content: "old", Modified: storedMod}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	snaps := &recordingSnapshotter{}
	svc := policyWriteSvc(w, WithRevisionSnapshotter(snaps))

	in := domain.Post{ID: 7, Author: self, Title: `<em></em>`, Content: `<script>x</script>`}
	stale := storedMod.Add(-time.Hour)
	err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, stale)
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("err = %v, want *ConflictError (sanitize must not run on conflict)", err)
	}
	if len(snaps.snapshots) != 0 {
		t.Error("snapshot must not run on conflict")
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called on conflict")
	}
}

// TestPostWriteUpdateEmptyTitleFailsClosed (task 5.2, Req 1.8, 4.2): when a
// changed title sanitizes to empty, Update fails closed with ErrTitleEmpty
// BEFORE the merge/write -- no unsanitized value reaches cur and no write
// happens. The snapshot has already run (it precedes sanitize in the sequence),
// so the historical row is preserved regardless.
func TestPostWriteUpdateEmptyTitleFailsClosed(t *testing.T) {
	const self = 5
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old title", Content: "old body"}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	svc := policyWriteSvc(w)

	in := domain.Post{ID: 7, Author: self, Title: `<em></em>`, Content: "new body"}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != ErrTitleEmpty {
		t.Fatalf("err = %v, want ErrTitleEmpty", err)
	}
	if w.updated != nil {
		t.Error("writer.Update must not be called when the title sanitizes to empty")
	}
}

// TestPostWriteUpdateUnchangedExcerptPassesThroughUnsanitized (task 5.2, Req
// 4.10) extends the Finding-3 byte-identity rule from content (covered by
// TestPostWriteUpdateUnchangedFieldPassesThroughUnsanitized) to the EXCERPT
// field: Req 4.10 states the skip for "a field's caller value byte-identical to
// the stored value", and the stored record carries all three sanitizable fields
// (content, excerpt, title). A sparse title-only edit therefore arrives with
// Excerpt == cur.Excerpt as the merge base, and that pre-M10a/imported excerpt
// must be written through byte-identically rather than re-sanitized -- a
// per-field implementation that applied the skip to content but not excerpt
// would still pass the content-only test while rewriting imported excerpts in
// place, which Requirement 5.2 forbids. Pairs with the content test to pin the
// rule at field granularity.
func TestPostWriteUpdateUnchangedExcerptPassesThroughUnsanitized(t *testing.T) {
	const self = 5
	// Both content and excerpt predate M10a and carry markup tier B would strip.
	storedContent := `<script>legacy()</script>`
	storedExcerpt := `<iframe src="x"></iframe>`
	stored := domain.Post{ID: 7, Author: self, Type: "post", Status: "draft",
		Title: "old title", Content: storedContent, Excerpt: storedExcerpt}
	w := &fakePostWriter{store: map[int64]domain.Post{7: stored}}
	svc := policyWriteSvc(w)

	// Title-only sparse edit: content and excerpt arrive == their stored values.
	in := domain.Post{ID: 7, Author: self, Title: "edited title",
		Content: storedContent, Excerpt: storedExcerpt}
	if err := svc.Update(context.Background(), actor(auth.RoleAuthor, self), in, time.Time{}); err != nil {
		t.Fatalf("update: %v", err)
	}
	if w.updated == nil {
		t.Fatal("writer.Update not called")
	}
	if w.updated.Excerpt != storedExcerpt {
		t.Errorf("unchanged excerpt was re-sanitized: got %q, want byte-identical %q (Req 4.10, Finding 3)", w.updated.Excerpt, storedExcerpt)
	}
	if w.updated.Content != storedContent {
		t.Errorf("unchanged content was re-sanitized: got %q, want byte-identical %q (Req 4.10, Finding 3)", w.updated.Content, storedContent)
	}
	if w.updated.Title != "edited title" {
		t.Errorf("title = %q, want %q", w.updated.Title, "edited title")
	}
}
