package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// These orchestration unit tests drive runBackfill over fake PostContentBackfill
// and PostContentUpdater implementations. They are the TDD-RED step for task
// 2.2: runBackfill, Report, backfillRowResult, Report.recordChanged/recordFailed,
// and printReport do not exist yet, so this file does NOT compile — which is the
// correct red. The failure names exactly those missing symbols, never a
// wrong-behavior assertion.
//
// THE SANITIZER SEAM. The design's runBackfill takes a concrete *sanitize.Policy
// (internal/sanitize/policy.go), and SanitizeAt over PostContent/PostExcerpt at
// TierB never errors — tier B routes straight through the compiled bluemonday
// policy, which returns (string, nil) unconditionally; only an unrecognised
// FieldKind/Tier fails closed (policy.go:121-148). A test cannot exercise the
// Requirement 9 fail-safe path through the real policy. So runBackfill is written
// to accept a tiny unexported seam interface the *sanitize.Policy already
// satisfies:
//
//	type fieldSanitizer interface {
//		SanitizeAt(sanitize.FieldKind, sanitize.Tier, string) (string, error)
//	}
//
// The real subcommand (task 3.2) passes sanitize.New() unchanged; these tests
// pass sanitize.New() for every case except the error case, which passes a fake
// that returns an error for a chosen field. Task 2.2 MUST implement runBackfill
// with this signature:
//
//	func runBackfill(ctx context.Context, reader domain.PostContentBackfill,
//		writer domain.PostContentUpdater, policy fieldSanitizer, apply bool) (Report, error)
//
// Report fields (per design Data Models):
//	Scanned, Changed, ContentChanges, ExcerptChanges, Failed int
//	Rows []backfillRowResult
//
// This keeps the seam change minimal: the only departure from the design's
// literal "*sanitize.Policy" parameter is widening it to the interface that type
// already implements, which is required to reach the fail-safe branch at all.

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakeBackfillReader serves seeded rows through BackfillPage honoring the keyset
// cursor (ID > afterID), the limit, and the type filter — the same contract the
// real *PostRepo.BackfillPage satisfies — so runBackfill's page walk and cursor
// advance are exercised rather than bypassed by a reader that hands back one
// slab. Rows are kept sorted by ID.
type fakeBackfillReader struct {
	rows  []domain.PostContentRow
	calls int
}

func newFakeReader(rows ...domain.PostContentRow) *fakeBackfillReader {
	// Insertion order is the ID order in these fixtures; the reader still
	// filters and paginates as the real port does.
	return &fakeBackfillReader{rows: rows}
}

func (f *fakeBackfillReader) BackfillPage(_ context.Context, afterID int64, limit int, types []string) ([]domain.PostContentRow, error) {
	f.calls++
	allow := map[string]bool{}
	for _, t := range types {
		allow[t] = true
	}
	var page []domain.PostContentRow
	for _, r := range f.rows {
		if r.ID <= afterID {
			continue
		}
		if !allow[r.Type] {
			continue
		}
		page = append(page, r)
		if len(page) == limit {
			break
		}
	}
	return page, nil
}

// fakeContentUpdater records every UpdateContentFields call: the ID and the exact
// content/excerpt it was handed. A test asserts both that it was (or was not)
// called and that each call carried BOTH clean values (Req 4.5).
type fakeContentUpdater struct {
	calls []updateCall
}

type updateCall struct {
	id      int64
	content string
	excerpt string
}

func (f *fakeContentUpdater) UpdateContentFields(_ context.Context, id int64, content, excerpt string) error {
	f.calls = append(f.calls, updateCall{id: id, content: content, excerpt: excerpt})
	return nil
}

func (f *fakeContentUpdater) calledFor(id int64) (updateCall, bool) {
	for _, c := range f.calls {
		if c.id == id {
			return c, true
		}
	}
	return updateCall{}, false
}

// errPolicy is the fieldSanitizer seam fake for the Requirement 9 fail-safe
// case: it delegates to a real policy except that it returns an error whenever
// the sanitized field is failField AND the input equals failInput. Keying the
// error on the specific input (not merely the field) lets ONE row fail while a
// later row of the same field sanitizes cleanly through the same policy, so the
// "sweep continues past a failed row" guarantee (Req 9.3) can be exercised. This
// provokes a SanitizeAt error without an unrecognised FieldKind/Tier (the only
// real error path).
type errPolicy struct {
	inner     *sanitize.Policy
	failField sanitize.FieldKind
	failInput string
	err       error
}

func (p errPolicy) SanitizeAt(k sanitize.FieldKind, t sanitize.Tier, in string) (string, error) {
	if k == p.failField && in == p.failInput {
		return "", p.err
	}
	return p.inner.SanitizeAt(k, t, in)
}

// ---------------------------------------------------------------------------
// Fixture values. These are chosen through the real tier-B policy so the test
// tracks the policy rather than a frozen literal: dirty* strips to clean*, and
// clean* is a tier-B fixed point (sanitizing it again yields itself).
// ---------------------------------------------------------------------------

const (
	dirtyContent = `<p>hi</p><script>alert('xss')</script>`
	dirtyExcerpt = `teaser<script>steal()</script>`
)

// tierBClean returns the tier-B-sanitized value of in for the given field,
// failing the test if sanitization errors (it cannot for PostContent/PostExcerpt
// at TierB, but the check keeps the fixture honest).
func tierBClean(t *testing.T, k sanitize.FieldKind, in string) string {
	t.Helper()
	out, err := sanitize.New().SanitizeAt(k, sanitize.TierB, in)
	if err != nil {
		t.Fatalf("fixture precondition: SanitizeAt(%v) on %q: %v", k, in, err)
	}
	return out
}

// ---------------------------------------------------------------------------
// Clean / changed / dry-run / apply matrix
// ---------------------------------------------------------------------------

// TestBackfillCleanRowIsScannedNotChanged covers Req 4.1 / 5.1: a row whose two
// fields are already tier-B fixed points is counted in Scanned, appends NO row
// result, and never triggers a write — under apply.
func TestBackfillCleanRowIsScannedNotChanged(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	reader := newFakeReader(domain.PostContentRow{ID: 1, Type: "post", Content: cleanC, Excerpt: cleanE})
	writer := &fakeContentUpdater{}

	rep, err := runBackfill(context.Background(), reader, writer, sanitize.New(), true)
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}
	if rep.Scanned != 1 {
		t.Errorf("Scanned = %d, want 1", rep.Scanned)
	}
	if rep.Changed != 0 {
		t.Errorf("Changed = %d, want 0 for an already-clean row", rep.Changed)
	}
	if len(rep.Rows) != 0 {
		t.Errorf("Rows = %d, want 0 — a clean row appends no row result (Req 7.1)", len(rep.Rows))
	}
	if len(writer.calls) != 0 {
		t.Errorf("writer called %d time(s) for a clean row, want 0 (Req 4.1)", len(writer.calls))
	}
}

// TestBackfillChangedRowsUnderApply covers Req 4.2 / 4.5 / 7.3: content-only,
// excerpt-only, and both-changed rows produce the right per-field and Changed
// totals and field markers, and under apply the writer is called with BOTH clean
// values even when only one field actually changed.
func TestBackfillChangedRowsUnderApply(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	cases := []struct {
		name               string
		row                domain.PostContentRow
		wantContentChanged bool
		wantExcerptChanged bool
	}{
		{
			name:               "content only",
			row:                domain.PostContentRow{ID: 10, Type: "post", Content: dirtyContent, Excerpt: cleanE},
			wantContentChanged: true,
			wantExcerptChanged: false,
		},
		{
			name:               "excerpt only",
			row:                domain.PostContentRow{ID: 11, Type: "page", Content: cleanC, Excerpt: dirtyExcerpt},
			wantContentChanged: false,
			wantExcerptChanged: true,
		},
		{
			name:               "both changed",
			row:                domain.PostContentRow{ID: 12, Type: "revision", Content: dirtyContent, Excerpt: dirtyExcerpt},
			wantContentChanged: true,
			wantExcerptChanged: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader := newFakeReader(tc.row)
			writer := &fakeContentUpdater{}

			rep, err := runBackfill(context.Background(), reader, writer, sanitize.New(), true)
			if err != nil {
				t.Fatalf("runBackfill: %v", err)
			}

			if rep.Scanned != 1 {
				t.Errorf("Scanned = %d, want 1", rep.Scanned)
			}
			if rep.Changed != 1 {
				t.Errorf("Changed = %d, want 1 — the row changed", rep.Changed)
			}
			wantContentCount, wantExcerptCount := 0, 0
			if tc.wantContentChanged {
				wantContentCount = 1
			}
			if tc.wantExcerptChanged {
				wantExcerptCount = 1
			}
			if rep.ContentChanges != wantContentCount {
				t.Errorf("ContentChanges = %d, want %d", rep.ContentChanges, wantContentCount)
			}
			if rep.ExcerptChanges != wantExcerptCount {
				t.Errorf("ExcerptChanges = %d, want %d", rep.ExcerptChanges, wantExcerptCount)
			}

			// Exactly one row result, naming which field(s) changed.
			if len(rep.Rows) != 1 {
				t.Fatalf("Rows = %d, want 1 reportable row", len(rep.Rows))
			}
			got := rep.Rows[0]
			if got.ID != tc.row.ID {
				t.Errorf("row result ID = %d, want %d", got.ID, tc.row.ID)
			}
			if got.Type != tc.row.Type {
				t.Errorf("row result Type = %q, want %q", got.Type, tc.row.Type)
			}
			if got.Failed {
				t.Errorf("row result marked Failed for a successfully sanitized row")
			}
			if got.ContentChanged != tc.wantContentChanged {
				t.Errorf("row.ContentChanged = %v, want %v", got.ContentChanged, tc.wantContentChanged)
			}
			if got.ExcerptChanged != tc.wantExcerptChanged {
				t.Errorf("row.ExcerptChanged = %v, want %v", got.ExcerptChanged, tc.wantExcerptChanged)
			}

			// Req 4.5: under apply, the writer is called once with BOTH clean
			// values — the clean content AND the clean excerpt — regardless of
			// which single field actually changed.
			call, ok := writer.calledFor(tc.row.ID)
			if !ok {
				t.Fatalf("writer not called for changed row %d under apply (Req 4.2)", tc.row.ID)
			}
			if len(writer.calls) != 1 {
				t.Errorf("writer called %d times, want exactly 1 for one changed row", len(writer.calls))
			}
			if call.content != cleanC {
				t.Errorf("writer content = %q, want the tier-B clean content %q (Req 4.5)", call.content, cleanC)
			}
			if call.excerpt != cleanE {
				t.Errorf("writer excerpt = %q, want the tier-B clean excerpt %q (Req 4.5)", call.excerpt, cleanE)
			}
		})
	}
}

// TestBackfillDryRunNeverWrites covers Req 6.1 / 6.2: a dry run records the
// changed rows in the Report exactly as an apply would, but NEVER calls the
// writer.
func TestBackfillDryRunNeverWrites(t *testing.T) {
	reader := newFakeReader(
		domain.PostContentRow{ID: 20, Type: "post", Content: dirtyContent, Excerpt: dirtyExcerpt},
		domain.PostContentRow{ID: 21, Type: "page", Content: dirtyContent, Excerpt: dirtyExcerpt},
	)
	writer := &fakeContentUpdater{}

	rep, err := runBackfill(context.Background(), reader, writer, sanitize.New(), false)
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}

	if rep.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", rep.Scanned)
	}
	if rep.Changed != 2 {
		t.Errorf("Changed = %d, want 2 — a dry run still records what it would change (Req 6.2)", rep.Changed)
	}
	if len(rep.Rows) != 2 {
		t.Errorf("Rows = %d, want 2 reportable rows on a dry run", len(rep.Rows))
	}
	if len(writer.calls) != 0 {
		t.Errorf("writer called %d time(s) on a DRY RUN, want 0 (Req 6.1)", len(writer.calls))
	}
}

// TestBackfillDryRunAndApplyNameIdenticalRows covers Req 6.3: a dry run and an
// apply over the same seeded fake name the identical rows and fields, so the
// preview faithfully predicts the apply.
func TestBackfillDryRunAndApplyNameIdenticalRows(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	seed := func() []domain.PostContentRow {
		return []domain.PostContentRow{
			{ID: 30, Type: "post", Content: dirtyContent, Excerpt: cleanE},           // content only
			{ID: 31, Type: "page", Content: cleanC, Excerpt: dirtyExcerpt},           // excerpt only
			{ID: 32, Type: "revision", Content: dirtyContent, Excerpt: dirtyExcerpt}, // both
			{ID: 33, Type: "post", Content: cleanC, Excerpt: cleanE},                 // clean, no row result
		}
	}

	dry, err := runBackfill(context.Background(), newFakeReader(seed()...), &fakeContentUpdater{}, sanitize.New(), false)
	if err != nil {
		t.Fatalf("dry runBackfill: %v", err)
	}
	app, err := runBackfill(context.Background(), newFakeReader(seed()...), &fakeContentUpdater{}, sanitize.New(), true)
	if err != nil {
		t.Fatalf("apply runBackfill: %v", err)
	}

	if dry.Scanned != app.Scanned || dry.Changed != app.Changed ||
		dry.ContentChanges != app.ContentChanges || dry.ExcerptChanges != app.ExcerptChanges ||
		dry.Failed != app.Failed {
		t.Errorf("dry-run and apply totals differ:\n dry  = %+v\n apply= %+v", dry, app)
	}
	if len(dry.Rows) != len(app.Rows) {
		t.Fatalf("dry-run named %d rows, apply named %d — the preview does not predict the apply (Req 6.3)",
			len(dry.Rows), len(app.Rows))
	}
	for i := range dry.Rows {
		d, a := dry.Rows[i], app.Rows[i]
		if d.ID != a.ID || d.Type != a.Type ||
			d.ContentChanged != a.ContentChanged || d.ExcerptChanged != a.ExcerptChanged ||
			d.Failed != a.Failed {
			t.Errorf("row %d differs between dry-run and apply:\n dry  = %+v\n apply= %+v", i, d, a)
		}
	}
}

// ---------------------------------------------------------------------------
// Fail-safe: a SanitizeAt error (Req 9.1-9.3)
// ---------------------------------------------------------------------------

// TestBackfillSanitizeErrorIsFailedAndSweepContinues covers Req 9.1-9.3: a row
// whose SanitizeAt errors is counted in Failed, named in the report by ID and
// failing field, never written (even under apply), and the walk continues to the
// next row. The error is provoked through the fieldSanitizer seam, since the real
// policy cannot error on PostContent/PostExcerpt at TierB.
func TestBackfillSanitizeErrorIsFailedAndSweepContinues(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	sentinel := errors.New("sanitize: boom")
	const failingContent = `<p>poison</p><script>boom()</script>`
	policy := errPolicy{inner: sanitize.New(), failField: sanitize.PostContent, failInput: failingContent, err: sentinel}

	reader := newFakeReader(
		domain.PostContentRow{ID: 40, Type: "post", Content: failingContent, Excerpt: dirtyExcerpt}, // content sanitization errors
		domain.PostContentRow{ID: 41, Type: "page", Content: dirtyContent, Excerpt: dirtyExcerpt},   // changes cleanly
	)
	writer := &fakeContentUpdater{}

	rep, err := runBackfill(context.Background(), reader, writer, policy, true)
	if err != nil {
		t.Fatalf("runBackfill must not abort the sweep on a per-row sanitize error: %v", err)
	}

	if rep.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2 — the walk continued past the failed row (Req 9.3)", rep.Scanned)
	}
	if rep.Failed != 1 {
		t.Errorf("Failed = %d, want 1 (Req 9.3)", rep.Failed)
	}
	if rep.Changed != 1 {
		t.Errorf("Changed = %d, want 1 — the second row still changed cleanly (Req 9.3)", rep.Changed)
	}

	// The failed row is not written; the clean second row is.
	if _, ok := writer.calledFor(40); ok {
		t.Errorf("writer was called for the failed row 40 — a sanitize error must not persist (Req 9.1)")
	}
	call, ok := writer.calledFor(41)
	if !ok {
		t.Errorf("writer not called for the still-clean row 41 after a prior row failed (Req 9.3)")
	} else if call.content != cleanC || call.excerpt != cleanE {
		t.Errorf("row 41 written with %q/%q, want clean %q/%q", call.content, call.excerpt, cleanC, cleanE)
	}

	// The failed row is named in the report by ID and failing field, distinct
	// from changed rows.
	var failedRow *backfillRowResult
	for i := range rep.Rows {
		if rep.Rows[i].Failed {
			failedRow = &rep.Rows[i]
			break
		}
	}
	if failedRow == nil {
		t.Fatalf("no row result marked Failed — the failed row must be reported (Req 9.2)")
	}
	if failedRow.ID != 40 {
		t.Errorf("failed row ID = %d, want 40 (Req 9.2)", failedRow.ID)
	}
	if !strings.Contains(strings.ToLower(failedRow.FailField), "content") {
		t.Errorf("failed row FailField = %q, want it to name post_content (Req 9.2)", failedRow.FailField)
	}
}

// ---------------------------------------------------------------------------
// Idempotence: a second apply, and a dry run after an apply (Req 5.1-5.3)
// ---------------------------------------------------------------------------

// TestBackfillSecondApplyIsNoOp covers Req 5.1 / 5.2: running the backfill over a
// fake already seeded with tier-B-clean rows (the state after a first apply) is a
// no-op — Changed == 0, both per-field counts 0, no writer call.
func TestBackfillSecondApplyIsNoOp(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	reader := newFakeReader(
		domain.PostContentRow{ID: 50, Type: "post", Content: cleanC, Excerpt: cleanE},
		domain.PostContentRow{ID: 51, Type: "page", Content: cleanC, Excerpt: cleanE},
	)
	writer := &fakeContentUpdater{}

	rep, err := runBackfill(context.Background(), reader, writer, sanitize.New(), true)
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}
	if rep.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", rep.Scanned)
	}
	if rep.Changed != 0 {
		t.Errorf("Changed = %d, want 0 — a second apply changes nothing (Req 5.1, 5.2)", rep.Changed)
	}
	if rep.ContentChanges != 0 || rep.ExcerptChanges != 0 {
		t.Errorf("per-field counts = content %d / excerpt %d, want 0/0 (Req 5.2)",
			rep.ContentChanges, rep.ExcerptChanges)
	}
	if len(writer.calls) != 0 {
		t.Errorf("writer called %d time(s) on a no-op second apply, want 0 (Req 5.1)", len(writer.calls))
	}
}

// TestBackfillDryRunAfterApplyReportsZero covers Req 5.3: a dry run over already-
// clean rows (the state after a completed apply) reports zero rows to change.
func TestBackfillDryRunAfterApplyReportsZero(t *testing.T) {
	cleanC := tierBClean(t, sanitize.PostContent, dirtyContent)
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	reader := newFakeReader(
		domain.PostContentRow{ID: 60, Type: "post", Content: cleanC, Excerpt: cleanE},
		domain.PostContentRow{ID: 61, Type: "revision", Content: cleanC, Excerpt: cleanE},
	)
	writer := &fakeContentUpdater{}

	rep, err := runBackfill(context.Background(), reader, writer, sanitize.New(), false)
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}
	if rep.Changed != 0 {
		t.Errorf("Changed = %d, want 0 — a dry run after an apply reports zero (Req 5.3)", rep.Changed)
	}
	if len(rep.Rows) != 0 {
		t.Errorf("Rows = %d, want 0 on a dry run over clean rows (Req 5.3)", len(rep.Rows))
	}
	if len(writer.calls) != 0 {
		t.Errorf("writer called on a dry run, want 0 (Req 6.1)")
	}
}

// ---------------------------------------------------------------------------
// printReport: the audit trail names rows/fields and totals, no bodies (Req 7)
// ---------------------------------------------------------------------------

// TestPrintReportNamesRowsFieldsAndTotals covers Req 7.1 / 7.3: printReport emits
// a per-row line naming the ID, post_type, and which field(s) changed, plus a
// totals block carrying the scanned/changed/per-field/failed counts. It is driven
// through runBackfill so the Report it formats is a real one.
func TestPrintReportNamesRowsFieldsAndTotals(t *testing.T) {
	cleanE := tierBClean(t, sanitize.PostExcerpt, dirtyExcerpt)

	reader := newFakeReader(
		domain.PostContentRow{ID: 70, Type: "post", Content: dirtyContent, Excerpt: cleanE}, // content only
	)
	rep, err := runBackfill(context.Background(), reader, &fakeContentUpdater{}, sanitize.New(), false)
	if err != nil {
		t.Fatalf("runBackfill: %v", err)
	}

	var buf strings.Builder
	printReport(&buf, rep, false)
	got := buf.String()
	if got == "" {
		t.Fatal("printReport wrote nothing")
	}

	// The per-row line names the ID and the changed field.
	if !strings.Contains(got, strconv.FormatInt(70, 10)) {
		t.Errorf("report does not name the changed row ID 70:\n%s", got)
	}
	if !strings.Contains(got, "post_content") {
		t.Errorf("report does not name the changed field post_content:\n%s", got)
	}

	// Totals block carries the counts.
	if !strings.Contains(strings.ToLower(got), "scanned") {
		t.Errorf("report has no scanned total:\n%s", got)
	}
	if !strings.Contains(strings.ToLower(got), "changed") {
		t.Errorf("report has no changed total:\n%s", got)
	}

	// Req 7.2: the full post bodies are never dumped. The dirty <script> body
	// and the raw alert payload must not appear in the report.
	if strings.Contains(got, "alert('xss')") || strings.Contains(got, dirtyContent) {
		t.Errorf("report dumped a full post body — Req 7.2 forbids it:\n%s", got)
	}
}
