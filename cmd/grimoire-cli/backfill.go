package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
)

// runBackfillContent runs the opt-in M14 sanitization backfill (Req 1). It is
// DRY RUN by default: without -apply it reports the rows/fields it WOULD rewrite
// and writes nothing (Req 1.2, 6). With -apply it performs the narrow
// content-only writes (Req 1.3). It never runs as part of normal server
// operation -- it is reachable only by invoking this subcommand (Req 1.4).
func runBackfillContent(args []string) error {
	fs := flag.NewFlagSet("backfill-content", flag.ExitOnError)
	cfgPath := fs.String("config", "configs/grimoire.sqlite.yaml", "path to grimoire config YAML")
	apply := fs.Bool("apply", false,
		"perform the writes; omit for a dry run that reports changes and writes nothing. "+
			"TAKE A DATABASE BACKUP before running with -apply: M14 provides no undo.") // Req 1.5
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err // nothing opened, nothing written (Req 1.6)
	}
	repos, err := storage.New(cfg.Database)
	if err != nil {
		return err // Req 1.6
	}
	defer repos.Close()

	policy := sanitize.New() // one policy, reused for every row (Req 2.1)
	report, err := runBackfill(context.Background(),
		repos.PostContentBackfill, repos.PostContentUpdater, policy, *apply)
	if err != nil {
		return err
	}
	printReport(os.Stdout, report, *apply)
	// Non-zero exit when any row failed sanitization during an apply, so the
	// failure is not lost in the report (Req 9.4). A dry run that merely
	// observed failures changed nothing, so it exits 0.
	if *apply && report.Failed > 0 {
		return fmt.Errorf("backfill-content: %d row(s) failed sanitization", report.Failed)
	}
	return nil
}

// fieldSanitizer is the minimal sanitizer seam runBackfill depends on. The real
// *sanitize.Policy (internal/sanitize/policy.go) already satisfies it, so the
// subcommand (task 3.2) passes sanitize.New() unchanged. Widening the parameter
// from the design's literal *sanitize.Policy to this interface is the one agreed
// departure: it is the only way a test can reach the Requirement 9 fail-safe
// branch, since SanitizeAt over PostContent/PostExcerpt at TierB never errors
// through the real compiled policy (policy.go:121-148).
type fieldSanitizer interface {
	SanitizeAt(sanitize.FieldKind, sanitize.Tier, string) (string, error)
}

// backfillRowResult is one reportable row: a row that changed or a row that
// failed sanitization. Clean (unchanged) rows produce no row result -- they are
// only counted in Report.Scanned (Req 7.1).
type backfillRowResult struct {
	ID             int64
	Type           string // post_type
	ContentChanged bool   // post_content differed from its tier-B value
	ExcerptChanged bool   // post_excerpt differed
	Failed         bool   // SanitizeAt errored on this row (Req 9.2)
	FailField      string // which field failed, when Failed
	// Size/diff indicator: before/after byte lengths, never the bodies (Req 7.1, 7.2).
	ContentBefore, ContentAfter int
	ExcerptBefore, ExcerptAfter int
}

// Report is the audit trail for a run (dry-run or apply). It is the ONLY record
// of what the sweep did, since M14 does not bump post_modified and offers no
// undo (D4, D5, Req 7.4).
type Report struct {
	Scanned        int // every targeted row read (Req 7.3)
	Changed        int // rows with at least one field differing (Req 7.3)
	ContentChanges int // per-field count for post_content (Req 7.3)
	ExcerptChanges int // per-field count for post_excerpt (Req 7.3)
	Failed         int // rows whose SanitizeAt errored (Req 9.3)
	Rows           []backfillRowResult
}

// recordChanged appends one row result for a row whose tier-B value differed in
// at least one field, and bumps the Changed total plus the matching per-field
// counts. The before/after byte lengths are the size/diff indicator (Req 7.1);
// the full bodies are never stored (Req 7.2).
func (rep *Report) recordChanged(row domain.PostContentRow, contentChanged, excerptChanged bool,
	contentBefore, contentAfter, excerptBefore, excerptAfter int) {

	rep.Changed++
	if contentChanged {
		rep.ContentChanges++
	}
	if excerptChanged {
		rep.ExcerptChanges++
	}
	rep.Rows = append(rep.Rows, backfillRowResult{
		ID:             row.ID,
		Type:           row.Type,
		ContentChanged: contentChanged,
		ExcerptChanged: excerptChanged,
		ContentBefore:  contentBefore,
		ContentAfter:   contentAfter,
		ExcerptBefore:  excerptBefore,
		ExcerptAfter:   excerptAfter,
	})
}

// recordFailed appends one row result for a row whose SanitizeAt errored, naming
// the failing field, and bumps the Failed total. The row is never written
// (Req 9.1-9.3). When both fields error, the content field is named first.
func (rep *Report) recordFailed(row domain.PostContentRow, errC, errE error) {
	failField := ""
	switch {
	case errC != nil:
		failField = "post_content"
	case errE != nil:
		failField = "post_excerpt"
	}
	rep.Failed++
	rep.Rows = append(rep.Rows, backfillRowResult{
		ID:        row.ID,
		Type:      row.Type,
		Failed:    true,
		FailField: failField,
	})
}

// runBackfill walks every targeted row page by page, applies tier B to both
// content fields, change-detects, and (when apply is set) writes the narrow
// content-only update for each changed row. It never aborts on one bad row: a
// SanitizeAt error marks that row failed and the walk continues (Req 9). It
// returns the accumulated Report; it writes nothing when apply is false (Req 6).
func runBackfill(ctx context.Context, reader domain.PostContentBackfill,
	writer domain.PostContentUpdater, policy fieldSanitizer, apply bool) (Report, error) {

	const pageSize = 500
	types := []string{"post", "page", "revision"} // D3 / Req 3.1; excludes attachment, nav_menu_item
	var rep Report
	var afterID int64

	for {
		page, err := reader.BackfillPage(ctx, afterID, pageSize, types)
		if err != nil {
			return rep, err // a read failure aborts the run (distinct from a per-row sanitize error)
		}
		if len(page) == 0 {
			break // walk complete (Req 3.4)
		}
		for _, row := range page {
			afterID = row.ID // advance the keyset cursor regardless of outcome
			rep.Scanned++

			cleanContent, errC := policy.SanitizeAt(sanitize.PostContent, sanitize.TierB, row.Content)
			cleanExcerpt, errE := policy.SanitizeAt(sanitize.PostExcerpt, sanitize.TierB, row.Excerpt)
			if errC != nil || errE != nil {
				// Fail-safe: report the row, do NOT write it, keep going (Req 9.1-9.3).
				rep.recordFailed(row, errC, errE)
				continue
			}

			contentChanged := cleanContent != row.Content
			excerptChanged := cleanExcerpt != row.Excerpt
			if !contentChanged && !excerptChanged {
				continue // already a tier-B fixed point: skip, no write (Req 4.1, 5.1)
			}

			rep.recordChanged(row, contentChanged, excerptChanged,
				len(row.Content), len(cleanContent), len(row.Excerpt), len(cleanExcerpt))

			if apply {
				// Narrow write: both clean values, only the two columns (Req 4.2-4.5).
				if err := writer.UpdateContentFields(ctx, row.ID, cleanContent, cleanExcerpt); err != nil {
					return rep, err // a write failure aborts (data-safety: stop, report, exit non-zero)
				}
			}
		}
	}
	return rep, nil
}

// printReport formats the accumulated Report to w: a per-row line for every
// changed/failed row (ID, post_type, which field(s), a byte-length before/after
// indicator) and a trailing totals block (scanned/changed/per-field/failed). It
// prints no post body (Req 7.2). It differs between a dry run and an apply only
// in a header verb -- the rows and fields it names are identical for the same
// input database (Req 6.3).
func printReport(w io.Writer, rep Report, apply bool) {
	verb := "would change"
	header := "DRY RUN"
	if apply {
		verb = "changed"
		header = "APPLY"
	}
	fmt.Fprintf(w, "backfill-content (%s): %d row(s) %s\n", header, rep.Changed, verb)

	for _, r := range rep.Rows {
		if r.Failed {
			fmt.Fprintf(w, "  FAILED  ID=%d post_type=%s field=%s (not written)\n",
				r.ID, r.Type, r.FailField)
			continue
		}
		fields := changedFieldList(r)
		fmt.Fprintf(w, "  %s  ID=%d post_type=%s fields=%s",
			verb, r.ID, r.Type, fields)
		if r.ContentChanged {
			fmt.Fprintf(w, " post_content[%dB->%dB]", r.ContentBefore, r.ContentAfter)
		}
		if r.ExcerptChanged {
			fmt.Fprintf(w, " post_excerpt[%dB->%dB]", r.ExcerptBefore, r.ExcerptAfter)
		}
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "totals: scanned=%d changed=%d post_content=%d post_excerpt=%d failed=%d\n",
		rep.Scanned, rep.Changed, rep.ContentChanges, rep.ExcerptChanges, rep.Failed)
}

// changedFieldList names which field(s) actually changed on a reportable row,
// comma-joined, for the per-row line (Req 7.1).
func changedFieldList(r backfillRowResult) string {
	switch {
	case r.ContentChanged && r.ExcerptChanged:
		return "post_content,post_excerpt"
	case r.ContentChanged:
		return "post_content"
	case r.ExcerptChanged:
		return "post_excerpt"
	default:
		return "none"
	}
}
