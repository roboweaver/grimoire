package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// reportPermalinks writes the resolved permalink structure, whether grimoire can
// serve it, the resolved archive bases and any non-fatal diagnostic about them,
// as part of "migrate -check"'s preflight report (M9a Req 4.5, M9b Req 4.6).
//
// It is strictly read-only: three option reads and a pure parse. Nothing here
// influences any migration path, and -check still writes nothing to the
// database.
//
// The point of reporting it here is discoverability. Without this, an
// unsupported structure or a colliding base is only announced by the server's
// startup WARN, which an operator adopting a database typically reads after the
// site is already serving 404s. -check is the surface they run first.
//
// The options are read with the same content.Option* names and through the same
// routing.Parse call the server's startup path uses, so the two cannot disagree
// about what "supported" means for a given database.
//
// The resolved category_base and tag_base are reported for every configuration,
// which replaces this function's pre-M9b silence about them. That silence was
// argued on the grounds that no route honored either base, so naming one would
// claim an override was in effect when it was not. This milestone makes both
// bases the seat of chi registration, of Classify's segment comparison and of
// every archive link, so the same sentence is now wrong in the opposite
// direction: -check is the only read-only surface where an operator who renamed
// a base can see which segment is actually being served (Req 4.6, 13.3).
//
// The structure verdict is a switch rather than the two early returns it used to
// be, which is what lets one base-and-notes report after it serve all three
// branches. routing.Parse resolves both bases -- and records their diagnostics --
// *before* it can fail or decide the structure is flat: the error path returns a
// flat copy that already carries them (internal/routing/routing.go). So the
// pre-M9b shape, where the unsupported branch and the Flat branch each returned
// before the end of the function, would have reported bases only for a resolved
// non-flat structure. The two configurations most operators actually have are the
// plain one and the one whose structure grimoire cannot parse, and both still
// serve /{CategoryBase}/{slug} and /{TagBase}/{slug}, so that is the branch where
// the report is needed least and the two it skipped are where it is needed most.
func reportPermalinks(ctx context.Context, w io.Writer, opts *content.OptionService) {
	raw := opts.Get(ctx, content.OptionPermalinkStructure)
	st, err := routing.Parse(
		raw,
		opts.Get(ctx, content.OptionCategoryBase),
		opts.Get(ctx, content.OptionTagBase),
	)
	switch {
	case err != nil:
		// State the consequence alongside the cause, matching the startup
		// warning: knowing a token was rejected is not the same as knowing
		// every published URL is about to 404 (Req 4.2, 4.4).
		fmt.Fprintf(w, "Permalink structure %q is NOT supported.\n", raw)
		fmt.Fprintf(w, "  %v\n", err)
		fmt.Fprintf(w, "  grimoire will fall back to the flat /{slug} route, "+
			"so published URLs will not resolve. Supported tokens: %s\n",
			strings.Join(routing.SupportedTokens(), " "))
	case st.Flat:
		fmt.Fprintf(w, "Permalink structure is plain (%s is empty), which is supported; "+
			"posts serve at the flat /{slug} route.\n", content.OptionPermalinkStructure)
	default:
		// ChiPatterns returns the no-slash form then the slash form, and
		// TrailingSlash decides which of the two is canonical. Reporting the
		// other one would name a path that 301s.
		patterns := st.ChiPatterns()
		canonical := patterns[0]
		if st.TrailingSlash {
			canonical = patterns[1]
		}
		fmt.Fprintf(w, "Permalink structure %q is supported; posts serve at %s.\n",
			st.Raw, canonical)
	}
	reportArchiveBases(w, st)
	reportPermalinkNotes(w, st)
}

// reportArchiveBases names the resolved category and tag archive bases (Req
// 4.6). It runs for every branch above, because the category and tag routes are
// registered from these bases whatever permalink_structure says -- a
// plain-permalink site with category_base set still serves its archives at that
// base.
func reportArchiveBases(w io.Writer, st routing.Structure) {
	fmt.Fprintln(w, "Archive bases, which apply to the configuration above whichever it is:")
	reportArchiveBase(w, content.OptionCategoryBase, "category", st.CategoryBase, st.CategoryBaseSet)
	reportArchiveBase(w, content.OptionTagBase, "tag", st.TagBase, st.TagBaseSet)
}

// reportArchiveBase writes one base line: the option, the segment it resolved to
// and where that segment came from.
//
// The origin half is worth a clause because the two cases are not the same
// configuration even when they print the same segment: an option set explicitly
// to "category" is set, and WordPress drops the permalink front for it, while an
// absent option keeps the front (Req 4.8, 4.9). The value quoted is the
// normalized one (Req 4.11), since that is the segment routes are registered at.
func reportArchiveBase(w io.Writer, option, kind, base string, set bool) {
	origin := fmt.Sprintf("the WordPress default, as %s is empty", option)
	if set {
		origin = fmt.Sprintf("read from %s", option)
	}
	fmt.Fprintf(w, "  %s: %q -- %s archives serve under /%s (%s)\n",
		option, base, kind, base, origin)
}

// reportPermalinkNotes prints every Structure.Notes() entry (Req 4.6). Notes are
// the non-fatal diagnostic channel: routing.Parse returns a usable Structure and
// a nil error for all of them, so without this the condition is invisible until
// an archive quietly stops resolving.
//
// Entries are printed verbatim, one per line. Each already names the option at
// fault, the value it resolved to and which of two competing readings won, so
// re-wording one here would only give an operator a second phrasing to match
// against the startup WARN. Nothing is printed when nothing collides -- a line
// seen on every run is one that stops being read, which would hide the entries
// that matter.
func reportPermalinkNotes(w io.Writer, st routing.Structure) {
	notes := st.Notes()
	if len(notes) == 0 {
		return
	}
	fmt.Fprintln(w, "Permalink option diagnostics:")
	for _, note := range notes {
		fmt.Fprintf(w, "  ! %s\n", note)
	}
}

// reportDuplicateNicenames reports every user_nicename shared by more than one
// user row, as part of "migrate -check"'s preflight report (Req 7.8).
//
// It is read-only: one aggregate query over {prefix}users, run once. That is the
// whole justification for reporting here rather than on the author archive
// route, where the same detection would be a COUNT on every hit for a condition
// that is nearly always absent (Req 7.8, and "Duplicate user_nicename" in the
// design). The auditor is the narrow domain.NicenameAuditor rather than
// domain.UserRepository, because this needs exactly one read and asking for the
// wide interface would put an operator-only diagnostic on the type every request
// path holds.
//
// Each conflict is reported as the nicename, how many rows share it and which ID
// wins under Req 7.6, on one line -- with several conflicts reported, counts and
// IDs on separate lines from the nicename they describe leave an operator
// pairing them by position. The consequence follows on its own line, because
// without it the numbers are trivia: the condition is invisible from outside,
// with no error and no wrong data, just one author whose posts have no URL
// (Req 7.7).
//
// Nothing is printed when there are no duplicates. wp_insert_user() suffixes a
// colliding nicename on write, so duplicates only arise from direct SQL, imports
// and multisite merges -- a "none found" line would appear on essentially every
// run, and a line an operator learns to skip hides the run that says something
// else. Same argument as reportPermalinkNotes above.
//
// A read error is reported and swallowed rather than returned. The permalink
// report above already declines to abort -check over a failed read, for the same
// reason: withholding the pending-migration summary that follows, over a line of
// diagnostics, would be the wrong trade.
func reportDuplicateNicenames(ctx context.Context, w io.Writer, auditor domain.NicenameAuditor) {
	conflicts, err := auditor.DuplicateNicenames(ctx)
	if err != nil {
		fmt.Fprintf(w, "Could not check for duplicate user_nicename values: %v\n", err)
		return
	}
	if len(conflicts) == 0 {
		return
	}
	fmt.Fprintln(w, "Duplicate user_nicename values, which grimoire resolves to the lowest ID:")
	for _, c := range conflicts {
		fmt.Fprintf(w, "  %q: %d user rows share it, and ID %d wins\n",
			c.Nicename, c.Count, c.WinnerID)
		fmt.Fprintf(w, "    /%s/%s serves ID %d only, so the other row's posts are "+
			"unreachable at that URL.\n", routing.AuthorBase, c.Nicename, c.WinnerID)
	}
}
