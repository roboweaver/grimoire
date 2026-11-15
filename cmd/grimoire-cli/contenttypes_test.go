package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
)

// Tests for the `migrate -check` undeclared-content-types diagnostic
// (M12 Req 11.1-11.4; D2, D9). They drive reportUndeclaredContentTypes over
// fakes of the narrow domain.PostTypeAuditor / domain.TaxonomyAuditor ports,
// as TestReportDuplicateNicenamesNamesEachConflict does for its auditor.

type fakePostTypeAuditor struct {
	rows  []domain.TypeCount
	err   error
	calls int
}

func (f *fakePostTypeAuditor) DistinctPostTypes(context.Context) ([]domain.TypeCount, error) {
	f.calls++
	return f.rows, f.err
}

type fakeTaxonomyAuditor struct {
	rows  []domain.TypeCount
	err   error
	calls int
}

func (f *fakeTaxonomyAuditor) DistinctTaxonomies(context.Context) ([]domain.TypeCount, error) {
	f.calls++
	return f.rows, f.err
}

func runUndeclared(t *testing.T, pt *fakePostTypeAuditor, tx *fakeTaxonomyAuditor, declared config.ContentConfig) string {
	t.Helper()
	var buf bytes.Buffer
	reportUndeclaredContentTypes(context.Background(), &buf, pt, tx, declared)
	return buf.String()
}

func TestReportUndeclaredContentTypesNamesUndeclaredPostTypeWithCountAndKey(t *testing.T) {
	pt := &fakePostTypeAuditor{rows: []domain.TypeCount{
		{Name: "post", Count: 10},
		{Name: "acf-field-group", Count: 18},
		{Name: "shop_order", Count: 1204},
	}}
	got := runUndeclared(t, pt, &fakeTaxonomyAuditor{}, config.ContentConfig{})

	for _, want := range []string{"shop_order", "1204", "acf-field-group", "18", "content.post_types"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q; got:\n%s", want, got)
		}
	}
	// Each name shares a line with its count, so an operator is not left
	// pairing them by position.
	for name, count := range map[string]string{"shop_order": "1204", "acf-field-group": "18"} {
		if !lineWith(got, name, count) {
			t.Errorf("%q and its count %s are not on one line; got:\n%s", name, count, got)
		}
	}
}

func TestReportUndeclaredContentTypesNamesUndeclaredTaxonomyWithCountAndKey(t *testing.T) {
	tx := &fakeTaxonomyAuditor{rows: []domain.TypeCount{
		{Name: "category", Count: 4},
		{Name: "genre", Count: 37},
	}}
	got := runUndeclared(t, &fakePostTypeAuditor{}, tx, config.ContentConfig{})

	for _, want := range []string{"genre", "37", "content.taxonomies"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q; got:\n%s", want, got)
		}
	}
	if !lineWith(got, "genre", "37") {
		t.Errorf("genre and its count are not on one line; got:\n%s", got)
	}
	if strings.Contains(got, "content.post_types") {
		t.Errorf("taxonomy-only report must not mention content.post_types; got:\n%s", got)
	}
}

func TestReportUndeclaredContentTypesSkipsReservedAndDeclared(t *testing.T) {
	var ptRows, txRows []domain.TypeCount
	for _, n := range registry.ReservedPostTypes() {
		ptRows = append(ptRows, domain.TypeCount{Name: n, Count: 3})
	}
	for _, n := range registry.ReservedTaxonomies() {
		txRows = append(txRows, domain.TypeCount{Name: n, Count: 3})
	}
	ptRows = append(ptRows, domain.TypeCount{Name: "event", Count: 5}, domain.TypeCount{Name: "book", Count: 6})
	txRows = append(txRows, domain.TypeCount{Name: "venue", Count: 7}, domain.TypeCount{Name: "genre", Count: 8})

	declared := config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{{Name: "event"}},
		Taxonomies: []config.TaxonomyConfig{{Name: "venue"}},
	}
	got := runUndeclared(t, &fakePostTypeAuditor{rows: ptRows}, &fakeTaxonomyAuditor{rows: txRows}, declared)

	for _, want := range []string{"book", "genre"} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing undeclared %q; got:\n%s", want, got)
		}
	}
	// Entry lines are indented; a header sentence may legitimately contain
	// the words "post" or "taxonomy", so reserved names are checked on entries only.
	var entries []string
	for _, l := range strings.Split(got, "\n") {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			entries = append(entries, l)
		}
	}
	entryText := strings.Join(entries, "\n")
	skipped := append(append([]string{}, registry.ReservedPostTypes()...), registry.ReservedTaxonomies()...)
	for _, name := range skipped {
		if hasToken(entryText, name) {
			t.Errorf("report names %q, which is reserved; got:\n%s", name, got)
		}
	}
	for _, name := range []string{"event", "venue"} {
		if hasToken(got, name) {
			t.Errorf("report names %q, which is declared and has rows; got:\n%s", name, got)
		}
	}
}

func TestReportUndeclaredContentTypesNotesDeclaredTypeWithNoRows(t *testing.T) {
	declared := config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{{Name: "event"}, {Name: "book"}},
		Taxonomies: []config.TaxonomyConfig{{Name: "venue"}, {Name: "genre"}},
	}
	pt := &fakePostTypeAuditor{rows: []domain.TypeCount{{Name: "book", Count: 2}}}
	tx := &fakeTaxonomyAuditor{rows: []domain.TypeCount{{Name: "genre", Count: 2}}}
	got := runUndeclared(t, pt, tx, declared)

	for _, want := range []string{"event", "venue"} {
		if !hasToken(got, want) {
			t.Errorf("declared type %q with no rows is not noted; got:\n%s", want, got)
		}
	}
	for _, name := range []string{"book", "genre"} {
		if hasToken(got, name) {
			t.Errorf("declared type %q has rows and must not be noted; got:\n%s", name, got)
		}
	}
}

func TestReportUndeclaredContentTypesIsSilentWhenNothingToSay(t *testing.T) {
	declared := config.ContentConfig{
		PostTypes:  []config.PostTypeConfig{{Name: "event"}},
		Taxonomies: []config.TaxonomyConfig{{Name: "venue"}},
	}
	cases := []struct {
		name string
		pt   []domain.TypeCount
		tx   []domain.TypeCount
		decl config.ContentConfig
	}{
		{"nil slices, nothing declared", nil, nil, config.ContentConfig{}},
		{"empty slices, nothing declared", []domain.TypeCount{}, []domain.TypeCount{}, config.ContentConfig{}},
		{
			"only reserved and declared names present",
			[]domain.TypeCount{{Name: "post", Count: 1}, {Name: "page", Count: 2}, {Name: "event", Count: 3}},
			[]domain.TypeCount{{Name: "category", Count: 1}, {Name: "venue", Count: 2}},
			declared,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runUndeclared(t, &fakePostTypeAuditor{rows: tc.pt}, &fakeTaxonomyAuditor{rows: tc.tx}, tc.decl)
			if got != "" {
				t.Fatalf("expected no output, got:\n%s", got)
			}
		})
	}
}

func TestReportUndeclaredContentTypesCallsEachAuditorOnce(t *testing.T) {
	pt := &fakePostTypeAuditor{rows: []domain.TypeCount{{Name: "a", Count: 1}, {Name: "b", Count: 2}}}
	tx := &fakeTaxonomyAuditor{rows: []domain.TypeCount{{Name: "c", Count: 1}, {Name: "d", Count: 2}}}
	_ = runUndeclared(t, pt, tx, config.ContentConfig{
		PostTypes: []config.PostTypeConfig{{Name: "x"}},
	})
	if pt.calls != 1 {
		t.Errorf("DistinctPostTypes called %d times, want 1", pt.calls)
	}
	if tx.calls != 1 {
		t.Errorf("DistinctTaxonomies called %d times, want 1", tx.calls)
	}
}

// A read error is reported as a "Could not check ..." line and swallowed. The
// other auditor is still consulted, and nothing is returned: the function has
// no result, so it cannot change the exit status (Req 11.3).
func TestReportUndeclaredContentTypesAbsorbsReadErrors(t *testing.T) {
	t.Run("post type read fails", func(t *testing.T) {
		pt := &fakePostTypeAuditor{err: errors.New("boom-pt")}
		tx := &fakeTaxonomyAuditor{rows: []domain.TypeCount{{Name: "genre", Count: 9}}}
		got := runUndeclared(t, pt, tx, config.ContentConfig{})
		if !strings.Contains(got, "Could not check") || !strings.Contains(got, "boom-pt") {
			t.Errorf("expected a 'Could not check' line carrying the error; got:\n%s", got)
		}
		if !strings.Contains(got, "genre") {
			t.Errorf("taxonomy report must still run after a post type read error; got:\n%s", got)
		}
		if tx.calls != 1 {
			t.Errorf("DistinctTaxonomies called %d times, want 1", tx.calls)
		}
	})
	t.Run("taxonomy read fails", func(t *testing.T) {
		pt := &fakePostTypeAuditor{rows: []domain.TypeCount{{Name: "book", Count: 9}}}
		tx := &fakeTaxonomyAuditor{err: errors.New("boom-tx")}
		got := runUndeclared(t, pt, tx, config.ContentConfig{})
		if !strings.Contains(got, "Could not check") || !strings.Contains(got, "boom-tx") {
			t.Errorf("expected a 'Could not check' line carrying the error; got:\n%s", got)
		}
		if !strings.Contains(got, "book") {
			t.Errorf("post type report must still run after a taxonomy read error; got:\n%s", got)
		}
	})
}

// Req 11.3: the report never returns an error. Pinned at compile time: the
// function has no result, so no caller can turn it into a failing exit status.
var _ func(context.Context, *bytes.Buffer, domain.PostTypeAuditor, domain.TaxonomyAuditor, config.ContentConfig) = func(
	ctx context.Context, w *bytes.Buffer, p domain.PostTypeAuditor, x domain.TaxonomyAuditor, d config.ContentConfig,
) {
	reportUndeclaredContentTypes(ctx, w, p, x, d)
}

// reportPreflight takes the declared configuration (D9). Compile-time pin.
var _ func(context.Context, *sql.DB, string, string, config.ContentConfig) error = reportPreflight

// runMigrate must hand reportPreflight the loaded cfg.Content, or the report
// would call every declared type undeclared.
func TestRunMigratePassesDeclaredContentToReportPreflight(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(src), "reportPreflight(ctx, db, vendor, prefix, cfg.Content)") {
		t.Error("runMigrate must call reportPreflight(ctx, db, vendor, prefix, cfg.Content)")
	}
}

// lineWith reports whether one output line contains both substrings.
func lineWith(out, a, b string) bool {
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, a) && strings.Contains(l, b) {
			return true
		}
	}
	return false
}

// hasToken reports whether name appears in out delimited by characters that
// cannot be part of a type name, so "post" does not match "post_types".
func hasToken(out, name string) bool {
	isName := func(r byte) bool {
		return r == '_' || r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
	}
	for i := 0; i+len(name) <= len(out); i++ {
		if out[i:i+len(name)] != name {
			continue
		}
		if i > 0 && isName(out[i-1]) {
			continue
		}
		if j := i + len(name); j < len(out) && isName(out[j]) {
			continue
		}
		return true
	}
	return false
}
