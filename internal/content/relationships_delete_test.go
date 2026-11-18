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

// --- M20: PostWriteService.Delete relationship purge (task 7.1, RED) ---------
//
// Written failing-first. They reference WithPostRelationshipPurger, which task
// 7.2 adds; until then this file fails to compile. Once stubbed, no
// PurgeTermRelationshipsOf call is recorded.
//
// The fixtures reuse the M18/M19 fakes (pdcLog, pdcWriter, pdcDetacher,
// pdcPurger, pdcRegistry, pdcAdmin, midPurger, midOptions) so the recorded
// order is one shared log. The existing delete-order tests configure no
// relationship purger, so by Req 10.9 their expected logs are unchanged.

var errRdInjected = errors.New("rd: injected failure")

// rdPurger records PurgeTermRelationshipsOf on the shared pdcLog.
type rdPurger struct {
	log     *pdcLog
	failing bool // records the call, then returns an error
}

func (p *rdPurger) PurgeTermRelationshipsOf(_ context.Context, id int64) error {
	err := p.log.record(fmt.Sprintf("relationships:%d", id))
	if err == nil && p.failing {
		return errRdInjected
	}
	return err
}

type rdOpts struct {
	noPurger bool
	failing  bool
	typ      string
}

func rdRun(t *testing.T, o rdOpts) ([]string, error) {
	t.Helper()
	log := &pdcLog{}
	w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: o.typ, Status: "publish", Author: 1, ParentID: pdcParent}}
	opts := []PostWriteOption{
		WithPostRegistry(pdcRegistry(t)),
		WithTermDetacher(&pdcDetacher{log: log}),
		WithStickyOptions(&midOptions{log: log}, &midOptions{log: log}),
		WithPostDependentsPurger(&pdcPurger{log: log}),
		WithPostMenuItemPurger(&midPurger{log: log}),
	}
	if !o.noPurger {
		opts = append(opts, WithPostRelationshipPurger(&rdPurger{log: log, failing: o.failing}))
	}
	err := NewPostWriteService(w, opts...).Delete(context.Background(), pdcAdmin(), domain.Post{ID: pdcID})
	return log.calls, err
}

// Req 10.7, D8: one purge, after the managed term detach, before unstick.
func TestRelationshipsDelete_OrderAndCallOnce(t *testing.T) {
	cases := []struct {
		typ  string
		want []string
	}{
		{"post", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
			"relationships:10", "unstick:10", "purge:10:7:", "menuitems:10:post", "delete:10"}},
		{"page", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0",
			"relationships:10", "purge:10:7:page", "menuitems:10:page", "delete:10"}},
		{"product", []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:genre:0",
			"relationships:10", "purge:10:7:", "menuitems:10:product", "delete:10"}},
	}
	for _, tc := range cases {
		t.Run(tc.typ, func(t *testing.T) {
			got, err := rdRun(t, rdOpts{typ: tc.typ})
			if err != nil {
				t.Fatalf("Delete err = %v, want nil", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

// Req 10.8: a purger error is returned, no later step or row delete is
// recorded, and a second Delete with a working purger completes.
func TestRelationshipsDelete_PurgerErrorThenRetry(t *testing.T) {
	got, err := rdRun(t, rdOpts{typ: "post", failing: true})
	if !errors.Is(err, errRdInjected) {
		t.Fatalf("Delete err = %v, want the purger error", err)
	}
	want := []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
		"relationships:10"}
	if !slices.Equal(got, want) {
		t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
	}
	got, err = rdRun(t, rdOpts{typ: "post"})
	if err != nil {
		t.Fatalf("retry Delete err = %v, want nil", err)
	}
	if n := len(got); n < 1 || got[n-1] != "delete:10" || !slices.Contains(got, "relationships:10") {
		t.Fatalf("retry log = %v, want the purge and then the row delete", got)
	}
}

// Req 10.9: with no purger configured the delete behaves as before M20.
func TestRelationshipsDelete_NoPurgerUnchanged(t *testing.T) {
	got, err := rdRun(t, rdOpts{typ: "page", noPurger: true})
	if err != nil {
		t.Fatalf("Delete err = %v, want nil", err)
	}
	want := []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0",
		"purge:10:7:page", "menuitems:10:page", "delete:10"}
	if !slices.Equal(got, want) {
		t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
	}
}

// Req 10.7: refused deletes call nothing beyond the load.
func TestRelationshipsDelete_RefusedCallsNothing(t *testing.T) {
	t.Run("unwritable type", func(t *testing.T) {
		got, err := rdRun(t, rdOpts{typ: "shop_order"})
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
			WithPostRelationshipPurger(&rdPurger{log: log}))
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
			WithPostRelationshipPurger(&rdPurger{log: log}))
		err := svc.Delete(context.Background(), pdcAdmin(), domain.Post{ID: 99})
		if !errors.Is(err, ErrForbidden) {
			t.Fatalf("Delete err = %v, want ErrForbidden", err)
		}
		if len(log.calls) != 0 {
			t.Fatalf("call log = %v, want no calls", log.calls)
		}
	})
}
