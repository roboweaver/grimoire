package content

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M22 task 3.1 (RED): PostWriteService Trash / Restore / Update / Delete --
//
// Written failing-first. They reference WithPostTrasher, WithTrashDays,
// TrashEnabled, Trash, Restore and ErrTrashUnsupported, which task 3.2 adds;
// until then this file fails to compile. Once stubbed, Update to "trash"
// writes the status directly and a trashed row is authorized as "trash".
//
// Every helper here carries the prefix trs so it cannot collide with the
// pdc/sticky helpers of the M18-M20 tests this file builds on.

const trsID int64 = 10

// trsLog is the ordered call log shared by the writer, the trasher and the
// sticky option fake, so the order of steps is observable.
type trsLog struct{ calls []string }

func (l *trsLog) add(s string) { l.calls = append(l.calls, s) }

func (l *trsLog) has(prefix string) bool {
	for _, c := range l.calls {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func (l *trsLog) index(prefix string) int {
	for i, c := range l.calls {
		if len(c) >= len(prefix) && c[:len(prefix)] == prefix {
			return i
		}
	}
	return -1
}

// trsWriter is a PostWriter + RevisionWriter over one stored row.
type trsWriter struct {
	domain.PostWriter
	domain.RevisionWriter
	log     *trsLog
	row     domain.Post
	missing bool
	updated []domain.Post
}

func (w *trsWriter) ByID(context.Context, int64) (domain.Post, error) {
	w.log.add("load")
	if w.missing {
		return domain.Post{}, domain.ErrNotFound
	}
	return w.row, nil
}

func (w *trsWriter) Update(_ context.Context, p domain.Post) error {
	w.log.add(fmt.Sprintf("update:%d:%s", p.ID, p.Status))
	w.updated = append(w.updated, p)
	return nil
}

func (w *trsWriter) DeleteRevisionsOf(_ context.Context, id int64) error {
	w.log.add(fmt.Sprintf("revisions:%d", id))
	return nil
}

func (w *trsWriter) Delete(_ context.Context, id int64) error {
	w.log.add(fmt.Sprintf("delete:%d", id))
	return nil
}

// trsTrasher records TrashPost and RestorePost in the shared log; TrashInfo
// reads are counted apart so they never disturb an order assertion.
type trsTrasher struct {
	log       *trsLog
	row       *domain.Post
	info      domain.TrashInfo
	infoCalls int
	trashPrev string
	restores  []domain.RestoreRequest
}

func (t *trsTrasher) TrashPost(_ context.Context, id int64, at time.Time) (string, error) {
	t.log.add(fmt.Sprintf("trash:%d", id))
	if at.IsZero() {
		return "", errors.New("trs: TrashPost called with a zero time")
	}
	return t.trashPrev, nil
}

func (t *trsTrasher) RestorePost(_ context.Context, id int64, req domain.RestoreRequest) error {
	t.log.add(fmt.Sprintf("restore:%d:%s:%q:%v", id, req.Status, req.Slug, req.HierarchicalScope))
	t.restores = append(t.restores, req)
	if t.row != nil && t.row.Status != "trash" {
		return domain.ErrNotTrashed
	}
	return nil
}

func (t *trsTrasher) TrashInfo(context.Context, int64) (domain.TrashInfo, error) {
	t.infoCalls++
	return t.info, nil
}

// trsOpts is a sticky_posts OptionRepository + OptionWriter.
type trsOpts struct {
	log    *trsLog
	val    string
	getErr error
}

func (o *trsOpts) Get(_ context.Context, name string) (string, error) {
	o.log.add("opt-get:" + name)
	if o.getErr != nil {
		return "", o.getErr
	}
	return o.val, nil
}

func (o *trsOpts) Set(_ context.Context, name, _ string) error {
	o.log.add("opt-set:" + name)
	return nil
}

func (o *trsOpts) Delete(context.Context, string) error {
	return errors.New("trs: option Delete must not be called")
}

// trsActor builds an actor holding the named capabilities.
func trsActor(caps ...string) auth.Principal { return auth.NewPrincipal(1, "actor", caps) }

// trsFullCaps lists every edit/delete/publish capability of one suffix.
func trsFullCaps(suffixes ...string) []string {
	var caps []string
	for _, suf := range suffixes {
		for _, verb := range []string{"edit", "publish", "delete", "edit_others", "delete_others",
			"edit_published", "delete_published", "edit_private", "delete_private"} {
			caps = append(caps, verb+"_"+suf)
		}
	}
	return caps
}

func trsAdmin() auth.Principal {
	return trsActor(trsFullCaps("posts", "pages", "products", "guides")...)
}

type trsCfg struct {
	row      domain.Post
	missing  bool
	days     int
	noStore  bool
	info     domain.TrashInfo
	sticky   bool
	stickyEr error
}

type trsRig struct {
	svc *PostWriteService
	w   *trsWriter
	tr  *trsTrasher
	log *trsLog
}

func trsBuild(t *testing.T, c trsCfg) *trsRig {
	t.Helper()
	log := &trsLog{}
	w := &trsWriter{log: log, row: c.row, missing: c.missing}
	tr := &trsTrasher{log: log, row: &w.row, info: c.info, trashPrev: c.row.Status}
	opts := []PostWriteOption{
		WithPostRegistry(pdcRegistry(t)),
		WithTrashDays(c.days),
	}
	if !c.noStore {
		opts = append(opts, WithPostTrasher(tr))
	}
	if c.sticky {
		o := &trsOpts{log: log, val: "a:1:{i:0;i:10;}", getErr: c.stickyEr}
		opts = append(opts, WithStickyOptions(o, o))
	}
	return &trsRig{svc: NewPostWriteService(w, opts...), w: w, tr: tr, log: log}
}

func trsRow(typ, status string) domain.Post {
	return domain.Post{ID: trsID, Type: typ, Status: status, Author: 1, ParentID: 7,
		Title: "Hello", Slug: "hello", Content: "body"}
}

// (a) Trash calls TrashPost once, in order, and runs no other step.
func TestTrashCallsTrashPostOnceForEveryManagedType(t *testing.T) {
	for _, typ := range []string{"post", "page", "product"} {
		t.Run(typ, func(t *testing.T) {
			r := trsBuild(t, trsCfg{row: trsRow(typ, "publish"), days: 30})
			if !r.svc.TrashEnabled() {
				t.Fatal("TrashEnabled = false, want true with a store and days > 0")
			}
			if err := r.svc.Trash(context.Background(), trsAdmin(), trsID); err != nil {
				t.Fatalf("Trash err = %v, want nil", err)
			}
			want := []string{"load", "trash:10"}
			if !slices.Equal(r.log.calls, want) {
				t.Fatalf("call log\n got: %v\nwant: %v", r.log.calls, want)
			}
		})
	}
}

func TestTrashUnsticksPostBeforeTheStoreCall(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30, sticky: true})
	if err := r.svc.Trash(context.Background(), trsAdmin(), trsID); err != nil {
		t.Fatalf("Trash err = %v, want nil", err)
	}
	want := []string{"load", "opt-get:sticky_posts", "opt-set:sticky_posts", "trash:10"}
	if !slices.Equal(r.log.calls, want) {
		t.Fatalf("call log\n got: %v\nwant: %v", r.log.calls, want)
	}
}

func TestTrashDoesNotUnstickNonPostTypes(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("page", "publish"), days: 30, sticky: true})
	if err := r.svc.Trash(context.Background(), trsAdmin(), trsID); err != nil {
		t.Fatalf("Trash err = %v, want nil", err)
	}
	if r.log.has("opt-") {
		t.Fatalf("a page must not touch sticky_posts: %v", r.log.calls)
	}
}

func TestTrashFailingUnstickRecordsNoStoreCall(t *testing.T) {
	boom := errors.New("trs: option read failed")
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30, sticky: true, stickyEr: boom})
	err := r.svc.Trash(context.Background(), trsAdmin(), trsID)
	if !errors.Is(err, boom) {
		t.Fatalf("Trash err = %v, want the unstick error", err)
	}
	if r.log.has("trash:") {
		t.Fatalf("TrashPost ran after a failed unstick: %v", r.log.calls)
	}
}

func TestTrashRunsNoRevisionDependentMenuOrRelationshipStep(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30, sticky: true})
	if err := r.svc.Trash(context.Background(), trsAdmin(), trsID); err != nil {
		t.Fatalf("Trash err = %v, want nil", err)
	}
	for _, p := range []string{"revisions:", "delete:", "update:", "terms:", "purge:"} {
		if r.log.has(p) {
			t.Fatalf("Trash ran a %q step: %v", p, r.log.calls)
		}
	}
}

// (b) Error precedence: forbidden, then unsupported, then already trashed.
func TestTrashForbiddenComesBeforeEveryOtherError(t *testing.T) {
	denied := trsActor("read")
	cases := []struct {
		name string
		cfg  trsCfg
	}{
		{"missing row", trsCfg{row: trsRow("post", "publish"), missing: true, days: 30}},
		{"unmanaged type", trsCfg{row: trsRow("movie", "publish"), days: 30}},
		{"forbidden actor, trash off", trsCfg{row: trsRow("post", "publish"), days: 0}},
		{"forbidden actor, no store", trsCfg{row: trsRow("post", "publish"), days: 30, noStore: true}},
		{"forbidden actor, already trashed", trsCfg{row: trsRow("post", "trash"), days: 30,
			info: domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := trsBuild(t, tc.cfg)
			err := r.svc.Trash(context.Background(), denied, trsID)
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Trash err = %v, want ErrForbidden", err)
			}
			if r.log.has("trash:") {
				t.Fatalf("TrashPost ran for a forbidden call: %v", r.log.calls)
			}
		})
	}
}

func TestTrashUnsupportedWhenNoStoreOrZeroDays(t *testing.T) {
	cases := map[string]trsCfg{
		"zero days": {row: trsRow("post", "publish"), days: 0},
		"no store":  {row: trsRow("post", "publish"), days: 30, noStore: true},
	}
	for name, cfg := range cases {
		t.Run(name, func(t *testing.T) {
			r := trsBuild(t, cfg)
			if r.svc.TrashEnabled() {
				t.Fatal("TrashEnabled = true, want false")
			}
			err := r.svc.Trash(context.Background(), trsAdmin(), trsID)
			if !errors.Is(err, ErrTrashUnsupported) {
				t.Fatalf("Trash err = %v, want ErrTrashUnsupported", err)
			}
		})
	}
}

func TestTrashDefaultServiceHasTrashOff(t *testing.T) {
	if NewPostWriteService(&trsWriter{log: &trsLog{}}).TrashEnabled() {
		t.Fatal("a service built without options must have trash off")
	}
}

func TestTrashAlreadyTrashedRowReturnsErrAlreadyTrashed(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "trash"), days: 30,
		info: domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}})
	err := r.svc.Trash(context.Background(), trsAdmin(), trsID)
	if !errors.Is(err, domain.ErrAlreadyTrashed) {
		t.Fatalf("Trash err = %v, want domain.ErrAlreadyTrashed", err)
	}
	if r.log.has("trash:") {
		t.Fatalf("TrashPost ran for an already trashed row: %v", r.log.calls)
	}
}

// (c) Update transition table.
func trsUpdateReq(row domain.Post, status string) domain.Post {
	p := row
	p.Status = status
	return p
}

func TestTrashUpdateLiveToLiveWritesOnly(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30})
	err := r.svc.Update(context.Background(), trsAdmin(), trsUpdateReq(r.w.row, "draft"), time.Time{})
	if err != nil {
		t.Fatalf("Update err = %v, want nil", err)
	}
	if r.log.has("trash:") || r.log.has("restore:") {
		t.Fatalf("live to live must not trash or restore: %v", r.log.calls)
	}
	if len(r.w.updated) != 1 || r.w.updated[0].Status != "draft" {
		t.Fatalf("updated = %+v, want one write with status draft", r.w.updated)
	}
}

func TestTrashUpdateLiveToTrashAppliesFieldsThenTrashes(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30})
	p := trsUpdateReq(r.w.row, "trash")
	p.Title = "Renamed"
	if err := r.svc.Update(context.Background(), trsAdmin(), p, time.Time{}); err != nil {
		t.Fatalf("Update err = %v, want nil", err)
	}
	if len(r.w.updated) != 1 {
		t.Fatalf("updated = %+v, want exactly one field write", r.w.updated)
	}
	if got := r.w.updated[0]; got.Status != "publish" || got.Title != "Renamed" {
		t.Fatalf("field write = status %q title %q, want status publish (unchanged) title Renamed", got.Status, got.Title)
	}
	u, tr := r.log.index("update:"), r.log.index("trash:10")
	if u < 0 || tr < 0 || u > tr {
		t.Fatalf("want the field write before TrashPost: %v", r.log.calls)
	}
}

func TestTrashUpdateLiveToTrashNeedsTheDeleteCapability(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "draft"), days: 30})
	editOnly := trsActor("edit_posts")
	err := r.svc.Update(context.Background(), editOnly, trsUpdateReq(r.w.row, "trash"), time.Time{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Update err = %v, want ErrForbidden", err)
	}
	if r.log.has("trash:") {
		t.Fatalf("TrashPost ran without the delete capability: %v", r.log.calls)
	}
}

func TestTrashUpdateTrashToTrashLeavesRowTrashed(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "trash"), days: 30,
		info: domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}})
	p := trsUpdateReq(r.w.row, "trash")
	p.Title = "Edited while trashed"
	if err := r.svc.Update(context.Background(), trsAdmin(), p, time.Time{}); err != nil {
		t.Fatalf("Update err = %v, want nil", err)
	}
	if r.log.has("trash:") || r.log.has("restore:") {
		t.Fatalf("trash to trash must neither trash nor restore: %v", r.log.calls)
	}
	if len(r.w.updated) != 1 || r.w.updated[0].Status != "trash" {
		t.Fatalf("updated = %+v, want one write with status trash", r.w.updated)
	}
}

func TestTrashUpdateOutOfTrashAppliesFieldsThenRestores(t *testing.T) {
	info := domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}
	for _, status := range []string{"draft", "publish"} {
		for _, typ := range []string{"post", "page"} {
			t.Run(typ+"/"+status, func(t *testing.T) {
				row := trsRow(typ, "trash")
				row.Slug = "hello__trashed"
				r := trsBuild(t, trsCfg{row: row, days: 30, info: info})
				p := trsUpdateReq(row, status)
				p.Title = "Back again"
				if err := r.svc.Update(context.Background(), trsAdmin(), p, time.Time{}); err != nil {
					t.Fatalf("Update err = %v, want nil", err)
				}
				if len(r.w.updated) != 1 || r.w.updated[0].Status != "trash" || r.w.updated[0].Title != "Back again" {
					t.Fatalf("updated = %+v, want one write with fields applied and status still trash", r.w.updated)
				}
				if len(r.tr.restores) != 1 {
					t.Fatalf("restores = %+v, want exactly one", r.tr.restores)
				}
				got := r.tr.restores[0]
				if got.Status != status || got.Slug != "" || got.HierarchicalScope != (typ == "page") || got.At.IsZero() {
					t.Fatalf("RestoreRequest = %+v, want status %s, empty slug, hierarchical=%v, non-zero At", got, status, typ == "page")
				}
				if u, rs := r.log.index("update:"), r.log.index("restore:"); u < 0 || rs < 0 || u > rs {
					t.Fatalf("want the field write before RestorePost: %v", r.log.calls)
				}
			})
		}
	}
}

func TestTrashUpdateOutOfTrashPassesAnExplicitSlugThrough(t *testing.T) {
	row := trsRow("post", "trash")
	row.Slug = "hello__trashed"
	r := trsBuild(t, trsCfg{row: row, days: 30,
		info: domain.TrashInfo{PreviousStatus: "draft", HasPreviousStatus: true}})
	p := trsUpdateReq(row, "draft")
	p.Slug = "brand-new"
	if err := r.svc.Update(context.Background(), trsAdmin(), p, time.Time{}); err != nil {
		t.Fatalf("Update err = %v, want nil", err)
	}
	if len(r.tr.restores) != 1 || r.tr.restores[0].Slug != "brand-new" {
		t.Fatalf("restores = %+v, want one with Slug brand-new", r.tr.restores)
	}
}

func TestTrashUpdateOutOfTrashToPublishNeedsThePublishCheck(t *testing.T) {
	row := trsRow("post", "trash")
	info := domain.TrashInfo{PreviousStatus: "draft", HasPreviousStatus: true}
	// The previous status (draft) passes with edit_posts alone; publishing does not.
	r := trsBuild(t, trsCfg{row: row, days: 30, info: info})
	err := r.svc.Update(context.Background(), trsActor("edit_posts"), trsUpdateReq(row, "publish"), time.Time{})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Update err = %v, want ErrForbidden", err)
	}
	if r.log.has("restore:") {
		t.Fatalf("RestorePost ran without the publish capability: %v", r.log.calls)
	}
	r = trsBuild(t, trsCfg{row: row, days: 30, info: info})
	ok := trsActor("edit_posts", "edit_published_posts", "publish_posts")
	if err := r.svc.Update(context.Background(), ok, trsUpdateReq(row, "publish"), time.Time{}); err != nil {
		t.Fatalf("Update err = %v, want nil with the publish capabilities", err)
	}
	if !r.log.has("restore:10:publish") {
		t.Fatalf("want RestorePost to publish: %v", r.log.calls)
	}
}

// (d) Restore status resolution.
func TestRestoreResolvesTheNewStatus(t *testing.T) {
	cases := []struct {
		name   string
		status string
		info   domain.TrashInfo
		want   string
	}{
		{"empty is draft", "", domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}, "draft"},
		{"previous uses the record", "previous", domain.TrashInfo{PreviousStatus: "pending", HasPreviousStatus: true}, "pending"},
		{"previous without a record is draft", "previous", domain.TrashInfo{}, "draft"},
		{"explicit status passes through", "publish", domain.TrashInfo{PreviousStatus: "draft", HasPreviousStatus: true}, "publish"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := trsBuild(t, trsCfg{row: trsRow("post", "trash"), days: 30, info: tc.info})
			if err := r.svc.Restore(context.Background(), trsAdmin(), trsID, tc.status); err != nil {
				t.Fatalf("Restore err = %v, want nil", err)
			}
			if len(r.tr.restores) != 1 {
				t.Fatalf("restores = %+v, want exactly one", r.tr.restores)
			}
			got := r.tr.restores[0]
			if got.Status != tc.want || got.Slug != "" || got.At.IsZero() {
				t.Fatalf("RestoreRequest = %+v, want status %q, empty slug, non-zero At", got, tc.want)
			}
		})
	}
}

func TestRestoreLiveRowReturnsErrNotTrashed(t *testing.T) {
	r := trsBuild(t, trsCfg{row: trsRow("post", "publish"), days: 30})
	err := r.svc.Restore(context.Background(), trsAdmin(), trsID, "")
	if !errors.Is(err, domain.ErrNotTrashed) {
		t.Fatalf("Restore err = %v, want domain.ErrNotTrashed", err)
	}
}

func TestRestoreForbiddenCases(t *testing.T) {
	info := domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}
	cases := []struct {
		name string
		cfg  trsCfg
		who  auth.Principal
	}{
		{"missing row", trsCfg{row: trsRow("post", "trash"), missing: true, days: 30, info: info}, trsAdmin()},
		{"unmanaged type", trsCfg{row: trsRow("movie", "trash"), days: 30, info: info}, trsAdmin()},
		{"no capability", trsCfg{row: trsRow("post", "trash"), days: 30, info: info}, trsActor("read")},
		// The previous status is publish, so edit_posts alone is not enough.
		{"previous status gates", trsCfg{row: trsRow("post", "trash"), days: 30, info: info}, trsActor("edit_posts")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := trsBuild(t, tc.cfg)
			err := r.svc.Restore(context.Background(), tc.who, trsID, "draft")
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Restore err = %v, want ErrForbidden", err)
			}
			if r.log.has("restore:") {
				t.Fatalf("RestorePost ran for a forbidden call: %v", r.log.calls)
			}
		})
	}
}

// (e) Delete of a trashed row: previous-status authorization, same step order.
func TestDeleteTrashedRowAuthorizesAsThePreviousStatus(t *testing.T) {
	info := domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}
	r := trsBuild(t, trsCfg{row: trsRow("post", "trash"), days: 30, info: info})
	// delete_posts alone would pass for a draft or the literal status "trash".
	err := r.svc.Delete(context.Background(), trsActor("delete_posts"), domain.Post{ID: trsID})
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("Delete err = %v, want ErrForbidden (previous status publish needs delete_published_posts)", err)
	}
	if r.log.has("delete:") || r.log.has("revisions:") {
		t.Fatalf("a forbidden Delete ran a step: %v", r.log.calls)
	}
	r = trsBuild(t, trsCfg{row: trsRow("post", "trash"), days: 30, info: info})
	ok := trsActor("delete_posts", "delete_published_posts")
	if err := r.svc.Delete(context.Background(), ok, domain.Post{ID: trsID}); err != nil {
		t.Fatalf("Delete err = %v, want nil", err)
	}
	if !r.log.has("delete:10") {
		t.Fatalf("Delete did not complete: %v", r.log.calls)
	}
}

func TestDeleteTrashedRowRunsTheSameStepsInTheSameOrder(t *testing.T) {
	log := &trsLog{}
	w := &trsWriter{log: log, row: trsRow("post", "trash")}
	tr := &trsTrasher{log: log, row: &w.row,
		info: domain.TrashInfo{PreviousStatus: "publish", HasPreviousStatus: true}}
	pl := &pdcLog{}
	svc := NewPostWriteService(w,
		WithPostRegistry(pdcRegistry(t)),
		WithTrashDays(30),
		WithPostTrasher(tr),
		WithTermDetacher(&pdcDetacher{log: pl}),
		WithPostDependentsPurger(&pdcPurger{log: pl}),
	)
	if err := svc.Delete(context.Background(), trsAdmin(), domain.Post{ID: trsID}); err != nil {
		t.Fatalf("Delete err = %v, want nil", err)
	}
	// Writer steps (load, revisions, delete) land in log, detach and purge in pl.
	wantW := []string{"load", "revisions:10", "delete:10"}
	if !slices.Equal(log.calls, wantW) {
		t.Fatalf("writer calls\n got: %v\nwant: %v", log.calls, wantW)
	}
	wantP := []string{"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0", "purge:10:7:"}
	if !slices.Equal(pl.calls, wantP) {
		t.Fatalf("detach/purge calls\n got: %v\nwant: %v", pl.calls, wantP)
	}
}
