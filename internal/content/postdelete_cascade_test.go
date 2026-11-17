package content

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
	"github.com/roboweaver/grimoire/internal/routing"
)

// --- M18: PostWriteService.Delete cascade (task 10.1, RED) -------------------
//
// Written failing-first. They reference WithPostDependentsPurger and
// domain.PostDependentsPurger, which tasks 9.2 and 10.2 add; until then this
// file fails to compile. Once stubbed, a `post` delete logs no term detach and
// no purge.

// pdcLog is the single ordered call log shared by every fake in this file.
type pdcLog struct {
	calls  []string
	failAt string // a call prefix; the call whose entry starts with it errors
}

var errPdcInjected = errors.New("pdc: injected failure")

// record appends entry and returns errPdcInjected when entry matches failAt.
func (l *pdcLog) record(entry string) error {
	l.calls = append(l.calls, entry)
	if l.failAt != "" && len(entry) >= len(l.failAt) && entry[:len(l.failAt)] == l.failAt {
		return errPdcInjected
	}
	return nil
}

// pdcWriter is a PostWriter + RevisionWriter that logs ByID, DeleteRevisionsOf
// and Delete.
type pdcWriter struct {
	domain.PostWriter
	domain.RevisionWriter
	log *pdcLog
	row domain.Post
}

func (w *pdcWriter) ByID(context.Context, int64) (domain.Post, error) {
	if err := w.log.record("load"); err != nil {
		return domain.Post{}, err
	}
	return w.row, nil
}

func (w *pdcWriter) DeleteRevisionsOf(_ context.Context, id int64) error {
	return w.log.record(fmt.Sprintf("revisions:%d", id))
}

func (w *pdcWriter) Delete(_ context.Context, id int64) error {
	return w.log.record(fmt.Sprintf("delete:%d", id))
}

// pdcDetacher is a PostTermsWriter that logs each SetPostTerms call.
type pdcDetacher struct{ log *pdcLog }

func (d *pdcDetacher) SetPostTerms(_ context.Context, id int64, tax string, terms []int64) error {
	return d.log.record(fmt.Sprintf("terms:%d:%s:%d", id, tax, len(terms)))
}

// pdcPurger is a PostDependentsPurger that logs each purge.
type pdcPurger struct{ log *pdcLog }

func (p *pdcPurger) PurgePostDependents(_ context.Context, id, parent int64, reparentType string) error {
	return p.log.record(fmt.Sprintf("purge:%d:%d:%s", id, parent, reparentType))
}

// pdcRegistry registers:
//
//	product  flat,         taxonomies [category, genre]
//	guide    hierarchical, taxonomies [topic] (by topic's object_types)
//	genre    object_types [product]
//	topic    object_types [post, page, guide]
//	badge    object_types [post]
func pdcRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	st, err := routing.Parse("/%postname%/", "", "")
	if err != nil {
		t.Fatalf("routing.Parse: %v", err)
	}
	yes := true
	reg, err := registry.Build(config.ContentConfig{
		PostTypes: []config.PostTypeConfig{
			{Name: "product", Public: &yes, Slug: "product", WithFront: &yes,
				CapabilityType: "product", Taxonomies: []string{"category", "genre"}},
			{Name: "guide", Public: &yes, Slug: "guide", WithFront: &yes,
				Hierarchical: true, CapabilityType: "guide"},
		},
		Taxonomies: []config.TaxonomyConfig{
			{Name: "genre", Public: &yes, Slug: "genre", WithFront: &yes, ObjectTypes: []string{"product"}},
			{Name: "topic", Public: &yes, Slug: "topic", WithFront: &yes, ObjectTypes: []string{"post", "page", "guide"}},
			{Name: "badge", Public: &yes, Slug: "badge", WithFront: &yes, ObjectTypes: []string{"post"}},
		},
	}, st)
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

func pdcAdmin() auth.Principal {
	var caps []string
	for _, suf := range []string{"posts", "pages", "products", "guides"} {
		for _, verb := range []string{"edit", "publish", "delete", "edit_others", "delete_others",
			"edit_published", "delete_published", "edit_private", "delete_private"} {
			caps = append(caps, verb+"_"+suf)
		}
	}
	return auth.NewPrincipal(1, "admin", caps)
}

const (
	pdcID     int64 = 10
	pdcParent int64 = 7
)

type pdcOpts struct {
	noDetacher bool
	noPurger   bool
	failAt     string
}

// pdcRun deletes a row of typ and returns the ordered call log and the error.
func pdcRun(t *testing.T, typ string, o pdcOpts) ([]string, error) {
	t.Helper()
	log := &pdcLog{failAt: o.failAt}
	w := &pdcWriter{log: log, row: domain.Post{ID: pdcID, Type: typ, Status: "publish", Author: 1, ParentID: pdcParent}}
	opts := []PostWriteOption{WithPostRegistry(pdcRegistry(t))}
	if !o.noDetacher {
		opts = append(opts, WithTermDetacher(&pdcDetacher{log: log}))
	}
	if !o.noPurger {
		opts = append(opts, WithPostDependentsPurger(&pdcPurger{log: log}))
	}
	err := NewPostWriteService(w, opts...).Delete(context.Background(), pdcAdmin(), domain.Post{ID: pdcID})
	return log.calls, err
}

// pdcCases lists the expected full log per stored type (Req 7.1-7.3, 9.1,
// 9.3, 10.1). Taxonomy order: built-ins, then registry taxonomies in
// declaration order; never a duplicate.
var pdcCases = []struct {
	typ  string
	want []string
}{
	{"post", []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "terms:10:badge:0",
		"purge:10:7:", "delete:10"}},
	{"page", []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0",
		"purge:10:7:page", "delete:10"}},
	{"product", []string{"load", "revisions:10",
		"terms:10:category:0", "terms:10:genre:0",
		"purge:10:7:", "delete:10"}},
	{"guide", []string{"load", "revisions:10",
		"terms:10:topic:0",
		"purge:10:7:guide", "delete:10"}},
}

func TestPostDeleteCascade_OrderAndTaxonomies(t *testing.T) {
	for _, tc := range pdcCases {
		t.Run(tc.typ, func(t *testing.T) {
			got, err := pdcRun(t, tc.typ, pdcOpts{})
			if err != nil {
				t.Fatalf("Delete err = %v, want nil", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, tc.want)
			}
		})
	}
}

func TestPostDeleteCascade_FailureStopsLaterSteps(t *testing.T) {
	for _, tc := range pdcCases {
		for i, step := range tc.want {
			if i == 0 { // "load" failing is the ByID path, not a cascade step
				continue
			}
			t.Run(fmt.Sprintf("%s/fail %s", tc.typ, step), func(t *testing.T) {
				got, err := pdcRun(t, tc.typ, pdcOpts{failAt: step})
				if !errors.Is(err, errPdcInjected) {
					t.Fatalf("Delete err = %v, want the injected error", err)
				}
				// The log ends at the failing step: nothing later ran.
				if want := tc.want[:i+1]; !slices.Equal(got, want) {
					t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
				}
				if slices.Contains(got, "delete:10") && step != "delete:10" {
					t.Fatalf("row deleted despite earlier failure at %q: %v", step, got)
				}
			})
		}
	}
}

func TestPostDeleteCascade_OptionalStepsSkipped(t *testing.T) {
	t.Run("no detacher skips terms only", func(t *testing.T) {
		got, err := pdcRun(t, "post", pdcOpts{noDetacher: true})
		if err != nil {
			t.Fatalf("Delete err = %v", err)
		}
		want := []string{"load", "revisions:10", "purge:10:7:", "delete:10"}
		if !slices.Equal(got, want) {
			t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
		}
	})
	t.Run("no purger skips purge only", func(t *testing.T) {
		got, err := pdcRun(t, "page", pdcOpts{noPurger: true})
		if err != nil {
			t.Fatalf("Delete err = %v", err)
		}
		want := []string{"load", "revisions:10",
			"terms:10:category:0", "terms:10:post_tag:0", "terms:10:topic:0", "delete:10"}
		if !slices.Equal(got, want) {
			t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
		}
	})
	t.Run("neither", func(t *testing.T) {
		got, err := pdcRun(t, "post", pdcOpts{noDetacher: true, noPurger: true})
		if err != nil {
			t.Fatalf("Delete err = %v", err)
		}
		want := []string{"load", "revisions:10", "delete:10"}
		if !slices.Equal(got, want) {
			t.Fatalf("call log mismatch\n got: %v\nwant: %v", got, want)
		}
	})
}

func TestPostDeleteCascade_UnwritableRefusedBeforeAnyStep(t *testing.T) {
	for _, typ := range []string{"shop_order", "attachment"} {
		t.Run(typ, func(t *testing.T) {
			got, err := pdcRun(t, typ, pdcOpts{})
			if !errors.Is(err, ErrForbidden) {
				t.Fatalf("Delete err = %v, want ErrForbidden", err)
			}
			if want := []string{"load"}; !slices.Equal(got, want) {
				t.Fatalf("call log = %v, want only the load", got)
			}
		})
	}
}
