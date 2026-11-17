package content

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M19 task 5: PostWriteService.Delete unsticks a deleted post (Req 8) ----

// stickyOpts is a recording OptionRepository + OptionWriter sharing the
// pdcLog so the order against the other delete steps is observable.
type stickyOpts struct {
	log    *pdcLog
	val    string
	exists bool
	getErr error
	writes []string
}

func (o *stickyOpts) Get(_ context.Context, name string) (string, error) {
	if err := o.log.record("opt-get:" + name); err != nil {
		return "", err
	}
	if o.getErr != nil {
		return "", o.getErr
	}
	if !o.exists {
		return "", domain.ErrNotFound
	}
	return o.val, nil
}

func (o *stickyOpts) Set(_ context.Context, name, value string) error {
	if err := o.log.record("opt-set:" + name); err != nil {
		return err
	}
	o.writes = append(o.writes, value)
	return nil
}

func (o *stickyOpts) Delete(context.Context, string) error {
	return errors.New("sticky: option Delete must not be called")
}

func stickyRun(t *testing.T, typ string, o *stickyOpts, failAt string) ([]string, error) {
	t.Helper()
	log := &pdcLog{failAt: failAt}
	o.log = log
	w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: typ, Status: "publish", Author: 1, ParentID: pdcParent}}
	err := NewPostWriteService(w,
		WithPostRegistry(pdcRegistry(t)),
		WithTermDetacher(&pdcDetacher{log: log}),
		WithPostDependentsPurger(&pdcPurger{log: log}),
		WithStickyOptions(o, o),
	).Delete(context.Background(), pdcAdmin(), domain.Post{ID: pdcID})
	return log.calls, err
}

func TestPostDeleteSticky_Branches(t *testing.T) {
	tests := []struct {
		name       string
		val        string
		exists     bool
		wantWrites []string
	}{
		{"id present, order kept", "a:3:{i:0;i:4;i:1;i:10;i:2;i:12;}", true, []string{"a:2:{i:0;i:4;i:1;i:12;}"}},
		{"id absent writes nothing", "a:2:{i:0;i:4;i:1;i:12;}", true, nil},
		{"no option writes nothing", "", false, nil},
		{"only id leaves empty array", "a:1:{i:0;i:10;}", true, []string{"a:0:{}"}},
		{"unparsable left alone", "garbage", true, nil},
		{"non-array left alone", "i:10;", true, nil},
		{"non-integer element left alone", `a:2:{i:0;i:10;i:1;s:1:"x";}`, true, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &stickyOpts{val: tc.val, exists: tc.exists}
			calls, err := stickyRun(t, "post", o, "")
			if err != nil {
				t.Fatalf("Delete err = %v, want nil", err)
			}
			if !slices.Equal(o.writes, tc.wantWrites) {
				t.Fatalf("writes = %v, want %v", o.writes, tc.wantWrites)
			}
			if calls[len(calls)-1] != "delete:10" {
				t.Fatalf("delete did not complete: %v", calls)
			}
		})
	}
}

func TestPostDeleteSticky_OrderAndNonPost(t *testing.T) {
	o := &stickyOpts{val: "a:1:{i:0;i:10;}", exists: true}
	calls, err := stickyRun(t, "post", o, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
		"opt-get:sticky_posts", "opt-set:sticky_posts",
		"purge:10:7:", "delete:10"}
	if !slices.Equal(calls, want) {
		t.Fatalf("order\n got: %v\nwant: %v", calls, want)
	}
	for _, typ := range []string{"page", "product", "guide"} {
		o := &stickyOpts{val: "a:1:{i:0;i:10;}", exists: true}
		calls, err := stickyRun(t, typ, o, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range calls {
			if len(c) >= 4 && c[:4] == "opt-" {
				t.Fatalf("%s delete touched options: %v", typ, calls)
			}
		}
	}
}

func TestPostDeleteSticky_FailuresAbort(t *testing.T) {
	for _, step := range []string{"opt-get:", "opt-set:"} {
		t.Run(step, func(t *testing.T) {
			o := &stickyOpts{val: "a:1:{i:0;i:10;}", exists: true}
			calls, err := stickyRun(t, "post", o, step)
			if !errors.Is(err, errPdcInjected) {
				t.Fatalf("err = %v, want injected", err)
			}
			for _, c := range calls {
				if c == "purge:10:7:" || c == "delete:10" {
					t.Fatalf("later step ran after %s failure: %v", step, calls)
				}
			}
		})
	}
	// A non-ErrNotFound read error aborts too.
	o := &stickyOpts{getErr: errors.New("boom")}
	if _, err := stickyRun(t, "post", o, ""); err == nil {
		t.Fatal("want read error returned")
	}
}

func TestPostDeleteSticky_NilPortsSkip(t *testing.T) {
	log := &pdcLog{}
	w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: "post", Status: "publish", Author: 1}}
	if err := NewPostWriteService(w, WithStickyOptions(nil, nil)).Delete(context.Background(), pdcAdmin(), domain.Post{ID: pdcID}); err != nil {
		t.Fatal(err)
	}
}
