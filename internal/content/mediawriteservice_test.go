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

// newMediaWriteServiceForTest builds a MediaWriteService over a REAL
// content.MediaService (so Create exercises the actual upload/Store path:
// filename sanitization, year/month layout, insert, rollback) wrapping the
// shared fakeMediaWriter/fakeMediaRepo from media_test.go. The upload directory
// is a per-test temp dir, and the MediaService clock is pinned so the generated
// year/month path is deterministic. The real sanitize.Policy is wired via
// WithMediaContentPolicy, matching how the process shares one *sanitize.Policy
// across the write services (Req 11.1).
func newMediaWriteServiceForTest(t *testing.T, allowed []string) (*MediaWriteService, *fakeMediaWriter) {
	t.Helper()
	root := t.TempDir()
	writer := &fakeMediaWriter{}
	repo := &fakeMediaRepo{}
	media := NewMediaService(repo, writer, MediaConfig{
		UploadsDir:   root,
		BaseURL:      "/wp-content/uploads",
		AllowedMIMEs: allowed,
	})
	media.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }
	svc := NewMediaWriteService(media, repo, writer, WithMediaContentPolicy(sanitize.New()))
	return svc, writer
}

// pngUpload is a minimal create input with an allowed-by-default image/png MIME.
func pngUpload() MediaCreateInput {
	return MediaCreateInput{Data: []byte("pngdata"), Filename: "cat.png", MimeType: "image/png"}
}

// TestMediaWriteCreateRequiresUploadFiles asserts a caller lacking upload_files
// (a subscriber) is rejected with ErrForbidden and NOTHING reaches the writer
// port -- the capability gate runs before any Store work (Req 5.1, 5.2).
func TestMediaWriteCreateRequiresUploadFiles(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, []string{"image/png"})

	_, err := svc.Create(context.Background(), actor(auth.RoleSubscriber, 7), pngUpload())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("subscriber create: err = %v, want ErrForbidden", err)
	}
	if len(writer.created) != 0 {
		t.Fatalf("forbidden create must not write: created = %+v", writer.created)
	}
}

// TestMediaWriteCreateDisallowedMIMERejected asserts a MIME outside
// AllowedMIMEs, for a caller WITHOUT unfiltered_upload, returns
// ErrUnsupportedMediaType and writes nothing (Req 6.1).
func TestMediaWriteCreateDisallowedMIMERejected(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, []string{"image/png"})

	in := MediaCreateInput{Data: []byte("%PDF-1.4"), Filename: "doc.pdf", MimeType: "application/pdf"}
	_, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 7), in)
	if !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("disallowed MIME: err = %v, want ErrUnsupportedMediaType", err)
	}
	if len(writer.created) != 0 {
		t.Fatalf("unsupported MIME must not write: created = %+v", writer.created)
	}
}

// TestMediaWriteCreateAllowedMIMEProceeds asserts an allowed MIME for an
// upload_files holder proceeds to Store and reaches the writer port (Req 1.1,
// 6.1).
func TestMediaWriteCreateAllowedMIMEProceeds(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, []string{"image/png"})

	m, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 7), pngUpload())
	if err != nil {
		t.Fatalf("allowed MIME create: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("allowed create should write once: created = %+v", writer.created)
	}
	if m.ID == 0 {
		t.Fatalf("returned media should carry the generated ID, got %+v", m)
	}
}

// TestMediaWriteCreateUnfilteredUploadBypassesMIME asserts a caller holding
// unfiltered_upload (administrator) may upload a MIME outside AllowedMIMEs --
// the allow-list is bypassed and the write proceeds (Req 6.2).
func TestMediaWriteCreateUnfilteredUploadBypassesMIME(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, []string{"image/png"})

	in := MediaCreateInput{Data: []byte("%PDF-1.4"), Filename: "doc.pdf", MimeType: "application/pdf"}
	if _, err := svc.Create(context.Background(), actor(auth.RoleAdministrator, 7), in); err != nil {
		t.Fatalf("unfiltered_upload create: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("unfiltered_upload should bypass MIME and write: created = %+v", writer.created)
	}
}

// TestMediaWriteCreateEmptyAllowListUnrestricted asserts an empty AllowedMIMEs
// is unrestricted for EVERY caller: an upload_files holder without
// unfiltered_upload may upload any MIME (Req 6.3).
func TestMediaWriteCreateEmptyAllowListUnrestricted(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, nil)

	in := MediaCreateInput{Data: []byte("%PDF-1.4"), Filename: "doc.pdf", MimeType: "application/pdf"}
	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, 7), in); err != nil {
		t.Fatalf("empty allow-list create: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("empty allow-list should be unrestricted: created = %+v", writer.created)
	}
}

// TestMediaWriteCreateThreadsAuthor asserts the acting principal's UserID is
// threaded into the created attachment's AuthorID -> post_author (D7, Req 1.4),
// so a later edit by the same caller is the own-attachment branch of Req 5.3.
func TestMediaWriteCreateThreadsAuthor(t *testing.T) {
	svc, writer := newMediaWriteServiceForTest(t, []string{"image/png"})

	const uid = int64(42)
	if _, err := svc.Create(context.Background(), actor(auth.RoleAuthor, uid), pngUpload()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(writer.created) != 1 {
		t.Fatalf("created attachments = %d, want 1", len(writer.created))
	}
	if got := writer.created[0].AuthorID; got != uid {
		t.Fatalf("stored AuthorID = %d, want %d (acting principal threaded to post_author)", got, uid)
	}
}

// TestMediaWriteSentinels pins the two new sentinels and the reuse of
// ErrForbidden. They are distinct error values so the transport can map
// ErrUnsupportedMediaType -> 415 and ErrUploadTooLarge -> 413 rather than a
// generic 400/500 (design "Sentinels").
func TestMediaWriteSentinels(t *testing.T) {
	if ErrUnsupportedMediaType == nil {
		t.Fatal("ErrUnsupportedMediaType must be defined")
	}
	if ErrUploadTooLarge == nil {
		t.Fatal("ErrUploadTooLarge must be defined")
	}
	// The three sentinels must be distinct so each maps to its own status.
	if errors.Is(ErrUnsupportedMediaType, ErrForbidden) ||
		errors.Is(ErrUploadTooLarge, ErrForbidden) ||
		errors.Is(ErrUnsupportedMediaType, ErrUploadTooLarge) {
		t.Fatal("ErrForbidden / ErrUnsupportedMediaType / ErrUploadTooLarge must be distinct sentinels")
	}
}

// ---------------------------------------------------------------------------
// Update unit tests (task 4.2). These are the failing-first (TDD red) tests for
// MediaWriteService.Update and MediaMetaUpdate, which do not exist yet (task
// 4.3). They compile-fail on those two symbols; that is the expected red.
//
// The service shape under test (design.md "content.MediaWriteService" Update):
//
//	Update(ctx, actor auth.Principal, id int64, upd MediaMetaUpdate) (domain.Media, error)
//	MediaMetaUpdate{Title, Caption, AltText, Description *string, Parent *int64}
//
// Every assertion pins behavior against the REAL sanitize.Policy (the oracle)
// and the shared fakeMediaWriter/fakeMediaRepo from media_test.go. Ownership is
// staged by seeding fakeMediaRepo.byID[id] with a stored domain.Media carrying a
// known AuthorID (post_author), so Update authorizes against the stored row and
// never a body value (Req 5.3-5.5, 11.2).

// strptr returns a pointer to s, for building the pointer-valued
// MediaMetaUpdate fields (nil = omitted/leave unchanged).
func strptr(s string) *string { return &s }

// int64ptr returns a pointer to n, for MediaMetaUpdate.Parent.
func int64ptr(n int64) *int64 { return &n }

// stageStoredMedia seeds the fakeMediaRepo behind the service with a stored
// attachment at id carrying post_author = authorID (plus any starting
// metadata), so Update's reader.ByID returns it and the ownership gate runs
// against this stored row. It returns the repo so a test can also stage a
// not-found id or inspect the filter.
func stageStoredMedia(repo *fakeMediaRepo, id, authorID int64, m domain.Media) {
	if repo.byID == nil {
		repo.byID = map[int64]domain.Media{}
	}
	m.ID = id
	m.AuthorID = authorID
	repo.byID[id] = m
}

// newMediaWriteUpdateForTest builds the service exactly like the create helper
// but returns the fakeMediaRepo too, since the update path reads the stored row
// by ID through the repo (reader.ByID) and the tests must seed byID/byIDErr.
func newMediaWriteUpdateForTest(t *testing.T) (*MediaWriteService, *fakeMediaRepo, *fakeMediaWriter) {
	t.Helper()
	root := t.TempDir()
	writer := &fakeMediaWriter{}
	repo := &fakeMediaRepo{}
	media := NewMediaService(repo, writer, MediaConfig{
		UploadsDir:   root,
		BaseURL:      "/wp-content/uploads",
		AllowedMIMEs: []string{"image/png"},
	})
	media.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }
	svc := NewMediaWriteService(media, repo, writer, WithMediaContentPolicy(sanitize.New()))
	return svc, repo, writer
}

// TestMediaWriteUpdateOwnAttachmentRequiresEditPosts asserts the own-attachment
// branch (Req 5.3): when actor.UserID == stored.AuthorID, editing requires
// edit_posts. An author (has edit_posts) authored the attachment, so the edit is
// authorized and reaches UpdateMeta; a subscriber (no edit_posts) who authored
// it is rejected with ErrForbidden and writes nothing. Ownership keys off the
// STORED post_author loaded by ID, never a body value (Req 5.5, 11.2).
func TestMediaWriteUpdateOwnAttachmentRequiresEditPosts(t *testing.T) {
	t.Run("author with edit_posts may edit own attachment", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		const uid = int64(7)
		stageStoredMedia(repo, 10, uid, domain.Media{Title: "orig"})

		if _, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
			Title: strptr("New Title"),
		}); err != nil {
			t.Fatalf("own-attachment edit by author: %v", err)
		}
		if len(writer.updateMeta) != 1 {
			t.Fatalf("authorized edit should write once: updateMeta = %+v", writer.updateMeta)
		}
	})

	t.Run("subscriber lacking edit_posts is forbidden on own attachment", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		const uid = int64(7)
		stageStoredMedia(repo, 10, uid, domain.Media{Title: "orig"})

		_, err := svc.Update(context.Background(), actor(auth.RoleSubscriber, uid), 10, MediaMetaUpdate{
			Title: strptr("New Title"),
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("subscriber own edit: err = %v, want ErrForbidden", err)
		}
		if len(writer.updateMeta) != 0 || len(writer.setParent) != 0 {
			t.Fatalf("forbidden edit must not write: updateMeta=%+v setParent=%+v", writer.updateMeta, writer.setParent)
		}
	})
}

// TestMediaWriteUpdateOtherAttachmentRequiresEditOthersPosts asserts the
// other-user branch (Req 5.4): when actor.UserID != stored.AuthorID, editing
// requires edit_others_posts. An editor (has edit_others_posts) may edit an
// attachment authored by someone else; an author (has edit_posts but NOT
// edit_others_posts) is rejected with ErrForbidden and writes nothing. The
// stored post_author is 999, distinct from both callers' IDs.
func TestMediaWriteUpdateOtherAttachmentRequiresEditOthersPosts(t *testing.T) {
	t.Run("editor with edit_others_posts may edit another's attachment", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 10, 999, domain.Media{Title: "orig"})

		if _, err := svc.Update(context.Background(), actor(auth.RoleEditor, 7), 10, MediaMetaUpdate{
			Title: strptr("New Title"),
		}); err != nil {
			t.Fatalf("other-attachment edit by editor: %v", err)
		}
		if len(writer.updateMeta) != 1 {
			t.Fatalf("authorized edit should write once: updateMeta = %+v", writer.updateMeta)
		}
	})

	t.Run("author lacking edit_others_posts is forbidden on another's attachment", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		stageStoredMedia(repo, 10, 999, domain.Media{Title: "orig"})

		_, err := svc.Update(context.Background(), actor(auth.RoleAuthor, 7), 10, MediaMetaUpdate{
			Title: strptr("New Title"),
		})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("author editing another's attachment: err = %v, want ErrForbidden", err)
		}
		if len(writer.updateMeta) != 0 || len(writer.setParent) != 0 {
			t.Fatalf("forbidden edit must not write: updateMeta=%+v setParent=%+v", writer.updateMeta, writer.setParent)
		}
	})
}

// TestMediaWriteUpdateMissingIDReturnsNotFound asserts Req 2.5: when
// fakeMediaRepo.ByID reports domain.ErrNotFound for {id}, Update returns
// domain.ErrNotFound (bubbled up) and performs no write.
func TestMediaWriteUpdateMissingIDReturnsNotFound(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	repo.byIDErr = map[int64]error{404: domain.ErrNotFound}

	_, err := svc.Update(context.Background(), actor(auth.RoleAdministrator, 1), 404, MediaMetaUpdate{
		Title: strptr("New Title"),
	})
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing id: err = %v, want domain.ErrNotFound", err)
	}
	if len(writer.updateMeta) != 0 || len(writer.setParent) != 0 {
		t.Fatalf("not-found update must not write: updateMeta=%+v setParent=%+v", writer.updateMeta, writer.setParent)
	}
}

// TestMediaWriteUpdatePerFieldSanitizeAtMappedKind asserts Req 4.1-4.4: each
// supplied field is sanitized at its mapped field kind, with the real
// sanitize.Policy as the oracle. A <script> in Caption (PostExcerpt, tier B) and
// in Description (PostContent, tier B) is stripped; a markup Title and AltText
// (both PostTitle, plain-text title path) are reduced to plain text. The actor
// is an author editing their own attachment so the gate passes and tier B is
// selected (no unfiltered_html).
func TestMediaWriteUpdatePerFieldSanitizeAtMappedKind(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	const uid = int64(7)
	stageStoredMedia(repo, 10, uid, domain.Media{})

	pol := sanitize.New()
	w := sanitize.For(actor(auth.RoleAuthor, uid))

	const (
		capIn  = `hi <script>alert(1)</script> there`
		descIn = `desc <script>alert(2)</script> end`
		titIn  = `<em>Hello</em> World`
		altIn  = `<b>Alt</b> text`
	)
	wantCap, err := pol.Sanitize(sanitize.PostExcerpt, w, capIn)
	if err != nil {
		t.Fatalf("oracle caption: %v", err)
	}
	wantDesc, err := pol.Sanitize(sanitize.PostContent, w, descIn)
	if err != nil {
		t.Fatalf("oracle description: %v", err)
	}
	wantTitle, err := pol.Sanitize(sanitize.PostTitle, w, titIn)
	if err != nil {
		t.Fatalf("oracle title: %v", err)
	}
	wantAlt, err := pol.Sanitize(sanitize.PostTitle, w, altIn)
	if err != nil {
		t.Fatalf("oracle alt: %v", err)
	}

	if _, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
		Title:       strptr(titIn),
		Caption:     strptr(capIn),
		AltText:     strptr(altIn),
		Description: strptr(descIn),
	}); err != nil {
		t.Fatalf("sanitizing update: %v", err)
	}
	if len(writer.updateMeta) != 1 {
		t.Fatalf("update should write once: updateMeta = %+v", writer.updateMeta)
	}
	got := writer.updateMeta[0].m

	// Script must be gone from the tier-B fields.
	if strings.Contains(got.Caption, "<script>") {
		t.Errorf("caption still contains <script>: %q", got.Caption)
	}
	if strings.Contains(got.Description, "<script>") {
		t.Errorf("description still contains <script>: %q", got.Description)
	}
	// Title/AltText reduced to plain text (no markup delimiters).
	if strings.ContainsAny(got.Title, "<>") {
		t.Errorf("title not reduced to plain text: %q", got.Title)
	}
	if strings.ContainsAny(got.AltText, "<>") {
		t.Errorf("alt text not reduced to plain text: %q", got.AltText)
	}
	// Each field matches the real policy at its mapped kind (the oracle).
	if got.Caption != wantCap {
		t.Errorf("caption = %q, want policy oracle %q (PostExcerpt, tier B)", got.Caption, wantCap)
	}
	if got.Description != wantDesc {
		t.Errorf("description = %q, want policy oracle %q (PostContent, tier B)", got.Description, wantDesc)
	}
	if got.Title != wantTitle {
		t.Errorf("title = %q, want policy oracle %q (PostTitle plain text)", got.Title, wantTitle)
	}
	if got.AltText != wantAlt {
		t.Errorf("alt = %q, want policy oracle %q (PostTitle plain text)", got.AltText, wantAlt)
	}
}

// TestMediaWriteUpdateUnfilteredHTMLPassesMarkupVerbatim asserts Req 4.5: a
// caller holding unfiltered_html (editor/administrator) sanitizes at tier C, so
// caption and description survive with markup intact. The input is one a tier-B
// caller WOULD alter (<script>), proving the pass-through is the tier-C bypass
// and not a coincidence. The editor edits their own attachment (edit_posts) so
// the gate passes.
func TestMediaWriteUpdateUnfilteredHTMLPassesMarkupVerbatim(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	const uid = int64(7)
	stageStoredMedia(repo, 10, uid, domain.Media{})

	const capIn = `keep <script>alert(1)</script> me`
	const descIn = `<div onclick="x()">rich</div>`

	// Sanity: a tier-B caller alters this input, so verbatim survival at tier C
	// is meaningful.
	pol := sanitize.New()
	anonW := sanitize.For(actor(auth.RoleAuthor, uid))
	if tierB, _ := pol.Sanitize(sanitize.PostExcerpt, anonW, capIn); tierB == capIn {
		t.Fatalf("precondition: tier B must alter %q for this test to be meaningful", capIn)
	}

	if _, err := svc.Update(context.Background(), actor(auth.RoleEditor, uid), 10, MediaMetaUpdate{
		Caption:     strptr(capIn),
		Description: strptr(descIn),
	}); err != nil {
		t.Fatalf("unfiltered_html update: %v", err)
	}
	if len(writer.updateMeta) != 1 {
		t.Fatalf("update should write once: updateMeta = %+v", writer.updateMeta)
	}
	got := writer.updateMeta[0].m
	if got.Caption != capIn {
		t.Errorf("unfiltered_html caption = %q, want verbatim %q (tier C)", got.Caption, capIn)
	}
	if got.Description != descIn {
		t.Errorf("unfiltered_html description = %q, want verbatim %q (tier C)", got.Description, descIn)
	}
}

// TestMediaWriteUpdateByteIdentitySkip asserts Req 2.4: re-sending the stored
// caption verbatim leaves it unchanged. The stored attachment carries a known
// caption; the update supplies that same byte string. The resulting UpdateMeta
// caption must equal the stored value (the sanitized-equals-stored path is a
// no-op at the value level, never a re-sanitization that could alter it).
func TestMediaWriteUpdateByteIdentitySkip(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	const uid = int64(7)
	const stored = "A plain stored caption"
	stageStoredMedia(repo, 10, uid, domain.Media{Caption: stored})

	if _, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
		Caption: strptr(stored),
	}); err != nil {
		t.Fatalf("byte-identity update: %v", err)
	}
	if len(writer.updateMeta) != 1 {
		t.Fatalf("update should write once: updateMeta = %+v", writer.updateMeta)
	}
	if got := writer.updateMeta[0].m.Caption; got != stored {
		t.Errorf("re-sent stored caption = %q, want unchanged %q (Req 2.4 byte-identity)", got, stored)
	}
}

// TestMediaWriteUpdateOmittedFieldsUntouched asserts Req 2.3: nil (omitted)
// fields leave the stored value untouched. The update supplies only Title; the
// UpdateMeta call must carry the STORED Caption/AltText/Description unchanged,
// since those pointers were nil and never reached the policy.
func TestMediaWriteUpdateOmittedFieldsUntouched(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	const uid = int64(7)
	stored := domain.Media{
		Title:       "old title",
		Caption:     "stored caption",
		AltText:     "stored alt",
		Description: "stored description",
	}
	stageStoredMedia(repo, 10, uid, stored)

	if _, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
		Title: strptr("Brand New Title"),
	}); err != nil {
		t.Fatalf("omitted-fields update: %v", err)
	}
	if len(writer.updateMeta) != 1 {
		t.Fatalf("update should write once: updateMeta = %+v", writer.updateMeta)
	}
	got := writer.updateMeta[0].m
	if got.Caption != stored.Caption {
		t.Errorf("omitted caption = %q, want stored %q (Req 2.3)", got.Caption, stored.Caption)
	}
	if got.AltText != stored.AltText {
		t.Errorf("omitted alt = %q, want stored %q (Req 2.3)", got.AltText, stored.AltText)
	}
	if got.Description != stored.Description {
		t.Errorf("omitted description = %q, want stored %q (Req 2.3)", got.Description, stored.Description)
	}
}

// TestMediaWriteUpdatePolicyErrorFailsClosed asserts Req 4.7 / 11.3: a title (or
// alt) that triggers sanitize.ErrTitleNotConverging returns the error unchanged
// and performs NO write (fail closed). The non-converging input is the one
// title.go's own test uses: many nested "amp;" layers around a single "lt;",
// which cannot reach an entity-decode fixed point within titleMaxPasses (16).
func TestMediaWriteUpdatePolicyErrorFailsClosed(t *testing.T) {
	nonConverging := "&" + strings.Repeat("amp;", 20) + "lt;"

	t.Run("non-converging Title fails closed", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		const uid = int64(7)
		stageStoredMedia(repo, 10, uid, domain.Media{})

		_, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
			Title: strptr(nonConverging),
		})
		if !errors.Is(err, sanitize.ErrTitleNotConverging) {
			t.Fatalf("non-converging title: err = %v, want sanitize.ErrTitleNotConverging", err)
		}
		if len(writer.updateMeta) != 0 || len(writer.setParent) != 0 {
			t.Fatalf("policy error must not write: updateMeta=%+v setParent=%+v", writer.updateMeta, writer.setParent)
		}
	})

	t.Run("non-converging AltText fails closed", func(t *testing.T) {
		svc, repo, writer := newMediaWriteUpdateForTest(t)
		const uid = int64(7)
		stageStoredMedia(repo, 10, uid, domain.Media{})

		_, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
			AltText: strptr(nonConverging),
		})
		if !errors.Is(err, sanitize.ErrTitleNotConverging) {
			t.Fatalf("non-converging alt: err = %v, want sanitize.ErrTitleNotConverging", err)
		}
		if len(writer.updateMeta) != 0 {
			t.Fatalf("policy error must not write: updateMeta=%+v", writer.updateMeta)
		}
	})
}

// TestMediaWriteUpdateSuppliedParentRoutesThroughSetParent asserts Req 2.2: a
// supplied Parent routes through the writer's SetParent port (parent is NOT part
// of MediaMeta / UpdateMeta, Req 3.5). The fake records the (id, parentID) call.
func TestMediaWriteUpdateSuppliedParentRoutesThroughSetParent(t *testing.T) {
	svc, repo, writer := newMediaWriteUpdateForTest(t)
	const uid = int64(7)
	stageStoredMedia(repo, 10, uid, domain.Media{})

	if _, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
		Parent: int64ptr(55),
	}); err != nil {
		t.Fatalf("parent update: %v", err)
	}
	if len(writer.setParent) != 1 {
		t.Fatalf("supplied parent should route through SetParent once: setParent = %+v", writer.setParent)
	}
	if writer.setParent[0].id != 10 || writer.setParent[0].parentID != 55 {
		t.Fatalf("SetParent recorded (%d, %d), want (10, 55)", writer.setParent[0].id, writer.setParent[0].parentID)
	}
}

// TestMediaWriteUpdateRereadsStoredValues asserts Req 10.3: the service re-reads
// via reader.ByID after the write, so the returned Media reflects the stored
// values a subsequent GET would observe. The fake returns a different Media on
// the second ByID; the returned value must be that re-read row, proving a second
// read happened after the write rather than the service echoing its input.
func TestMediaWriteUpdateRereadsStoredValues(t *testing.T) {
	const uid = int64(7)
	// A repo whose ByID returns the staged row first and an "updated" row after
	// the write, so the test can distinguish a re-read from an echo.
	repo := &rereadMediaRepo{
		first:  domain.Media{ID: 10, AuthorID: uid, Title: "before"},
		second: domain.Media{ID: 10, AuthorID: uid, Title: "AFTER REREAD"},
	}
	root := t.TempDir()
	writer := &fakeMediaWriter{}
	media := NewMediaService(repo, writer, MediaConfig{UploadsDir: root, BaseURL: "/wp-content/uploads", AllowedMIMEs: []string{"image/png"}})
	media.now = func() time.Time { return time.Date(2026, 8, 23, 9, 0, 0, 0, time.UTC) }
	svc := NewMediaWriteService(media, repo, writer, WithMediaContentPolicy(sanitize.New()))

	out, err := svc.Update(context.Background(), actor(auth.RoleAuthor, uid), 10, MediaMetaUpdate{
		Title: strptr("New Title"),
	})
	if err != nil {
		t.Fatalf("re-read update: %v", err)
	}
	if repo.byIDCalls < 2 {
		t.Fatalf("ByID called %d times, want >= 2 (load + re-read after write, Req 10.3)", repo.byIDCalls)
	}
	if out.Title != "AFTER REREAD" {
		t.Fatalf("returned Media.Title = %q, want the re-read %q (Req 10.3)", out.Title, "AFTER REREAD")
	}
}

// rereadMediaRepo is a MediaRepo whose ByID returns `first` on the initial load
// and `second` on every subsequent call, counting calls so the re-read assertion
// (Req 10.3) can prove a second read happened after the write. Only ByID differs
// from fakeMediaRepo; the List/Count methods satisfy the port with trivial
// returns.
type rereadMediaRepo struct {
	first     domain.Media
	second    domain.Media
	byIDCalls int
}

func (r *rereadMediaRepo) ByID(_ context.Context, _ int64) (domain.Media, error) {
	r.byIDCalls++
	if r.byIDCalls == 1 {
		return r.first, nil
	}
	return r.second, nil
}

func (r *rereadMediaRepo) List(_ context.Context, _ domain.MediaFilter) ([]domain.Media, error) {
	return nil, nil
}

func (r *rereadMediaRepo) Count(_ context.Context, _ domain.MediaFilter) (int, error) {
	return 0, nil
}
