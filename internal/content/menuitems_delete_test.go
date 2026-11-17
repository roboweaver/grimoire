package content

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// --- M19: PostWriteService.Delete menu-item purge (task 10.1, RED) -----------
//
// Written failing-first. They reference WithPostMenuItemPurger and
// domain.PostMenuItemPurger, which tasks 9.2 and 10.2 add; until then this file
// fails to compile. Once stubbed, no PurgeMenuItemsOf call is recorded.
//
// The fixtures reuse the M18 cascade fakes (pdcLog, pdcWriter, pdcDetacher,
// pdcPurger, pdcRegistry, pdcAdmin) from postdelete_cascade_test.go so the
// recorded order is one shared log. The existing M18 delete-order tests are NOT
// edited: none of them configures a menu-item purger, so by Req 10.8 their
// expected logs are unchanged.

var errMidInjected = errors.New("mid: injected failure")

// midPurger records PurgeMenuItemsOf on the shared pdcLog.
type midPurger struct {
	log     *pdcLog
	failing bool // when true, the purge records its call and returns an error
}

func (p *midPurger) PurgeMenuItemsOf(_ context.Context, id int64, typ string) error {
	err := p.log.record(fmt.Sprintf("menuitems:%d:%s", id, typ))
	if err == nil && p.failing {
		return errMidInjected
	}
	return err
}

// midOptions is an OptionRepository + OptionWriter holding sticky_posts = [10].
type midOptions struct{ log *pdcLog }

func (o *midOptions) Get(context.Context, string) (string, error) {
	return "a:1:{i:0;i:10;}", nil
}

func (o *midOptions) Set(_ context.Context, _, _ string) error { return o.log.record("unstick:10") }

func (o *midOptions) Delete(context.Context, string) error { return nil }

type midOpts struct {
	noPurger bool
	failing  bool   // the menu-item purger errors
	failAt   string // an earlier step (pdcLog prefix) errors
	typ      string
}

// midRun deletes a row and returns the ordered call log and the error.
func midRun(t *testing.T, o midOpts) ([]string, error) {
	t.Helper()
	log := &pdcLog{failAt: o.failAt}
	w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: o.typ, Status: "publish", Author: 1, ParentID: pdcParent}}
	opts := []PostWriteOption{
		WithPostRegistry(pdcRegistry(t)),
		WithTermDetacher(&pdcDetacher{log: log}),
		WithStickyOptions(&midOptions{log: log}, &midOptions{log: log}),
		WithPostDependentsPurger(&pdcPurger{log: log}),
	}
	if !o.noPurger {
		opts = append(opts, WithPostMenuItemPurger(&midPurger{log: log, failing: o.failing}))
	}
	err := NewPostWriteService(w, opts...).Delete(context.Background(), pdcAdmin(), domain.Post{ID: pdcID})
	return log.calls, err
}

// Req 10.1, D8: one PurgeMenuItemsOf with the stored type, after the dependents
// purge and before the row delete.
func TestMenuItemsDelete_OrderAndStoredType(t *testing.T) {
	cases := []struct {
		typ  string
		want []string
	}{
		{"post", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
			"unstick:10", "purge:10:7:", "menuitems:10:post", "delete:10"}},
		{"page", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0",
			"purge:10:7:page", "menuitems:10:page", "delete:10"}},
		{"product", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:genre:0",
			"purge:10:7:", "menuitems:10:product", "delete:10"}},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			got, err := midRun(t, midOpts{typ: tc.typ})
			if err != nil {
				t.Fatalf("Delete err = %v, want nil", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

// Req 10.7: a purger error is returned, no row delete is recorded, and a second
// Delete with a working purger completes.
func TestMenuItemsDelete_PurgerErrorThenRetry(t *testing.T) {
	got, err := midRun(t, midOpts{typ: "page", failing: true})
	if !errors.Is(err, errMidInjected) {
		t.Fatalf("Delete err = %v, want the purger error", err)
	}
	want := []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0",
		"purge:10:7:page", "menuitems:10:page"}
	if !slices.Equal(got, want) {
		t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
	}

	got, err = midRun(t, midOpts{typ: "page"})
	if err != nil {
		t.Fatalf("retry Delete err = %v, want nil", err)
	}
	if n := len(got); n < 2 || got[n-2] != "menuitems:10:page" || got[n-1] != "delete:10" {
		t.Fatalf("retry log = %v, want it to end with the purge then the row delete", got)
	}
}

// Req 10.1, 10.7: a failing earlier step records no menu-item purge.
func TestMenuItemsDelete_EarlierFailureSkipsPurge(t *testing.T) {
	steps := []struct{ typ, failAt string }{
		{"post", "revisions:"},
		{"post", "terms:"},
		{"post", "unstick:"},
		{"post", "purge:"},
		{"page", "purge:"},
	}
	for _, tc := range steps {
		t.Run(tc.typ+"/fail "+tc.failAt, func(t *testing.T) {
			got, err := midRun(t, midOpts{typ: tc.typ, failAt: tc.failAt})
			if !errors.Is(err, errPdcInjected) {
				t.Fatalf("Delete err = %v, want the injected error", err)
			}
			for _, c := range got {
				if len(c) >= 10 && c[:10] == "menuitems:" {
					t.Fatalf("menu-item purge recorded after an earlier failure: %v", got)
				}
			}
		})
	}
}

// Req 10.1: refused deletes call nothing beyond the load.
func TestMenuItemsDelete_RefusedCallsNothing(t *testing.T) {
	t.Run("unwritable type", func(t *testing.T) {
		got, err := midRun(t, midOpts{typ: "shop_order"})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("Delete err = %v, want ErrForbidden", err)
		}
		if want := []string{"load"}; !slices.Equal(got, want) {
			t.Fatalf("call log = %v, want only the load", got)
		}
	})

	t.Run("forbidden actor", func(t *testing.T) {
		log := &pdcLog{}
		w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: "post", Status: "publish", Author: 1}}
		svc := NewPostWriteService(w,
			WithPostRegistry(pdcRegistry(t)),
			WithPostMenuItemPurger(&midPurger{log: log}))
		err := svc.Delete(context.Background(), auth.NewPrincipal(2, "nobody", nil), domain.Post{ID: pdcID})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("Delete err = %v, want ErrForbidden", err)
		}
		if want := []string{"load"}; !slices.Equal(log.calls, want) {
			t.Fatalf("call log = %v, want only the load", log.calls)
		}
	})

	t.Run("missing id", func(t *testing.T) {
		log := &pdcLog{}
		w := &midMissingWriter{pdcWriter: pdcWriter{log: log}}
		svc := NewPostWriteService(w,
			WithPostRegistry(pdcRegistry(t)),
			WithPostMenuItemPurger(&midPurger{log: log}))
		err := svc.Delete(context.Background(), pdcAdmin(), domain.Post{ID: 99})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("Delete err = %v, want ErrForbidden", err)
		}
		if len(log.calls) != 0 {
			t.Fatalf("call log = %v, want no calls", log.calls)
		}
	})
}

// midMissingWriter reports every id as not found.
type midMissingWriter struct{ pdcWriter }

func (*midMissingWriter) ByID(context.Context, int64) (domain.Post, error) {
	return domain.Post{}, domain.ErrNotFound
}

// Req 10.8: with no menu-item purger the delete behaves as before M19.
func TestMenuItemsDelete_NoPurgerUnchanged(t *testing.T) {
	got, err := midRun(t, midOpts{typ: "post", noPurger: true})
	if err != nil {
		t.Fatalf("Delete err = %v, want nil", err)
	}
	want := []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
		"unstick:10", "purge:10:7:", "delete:10"}
	if !slices.Equal(got, want) {
		t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
	}
}
