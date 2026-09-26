package main

import (
	"context"
	"log/slog"
	"strings"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/routing"
)

// resolvePermalinks reads the permalink option set once and returns the
// Structure the server and the REST mapper both use.
//
// The three options are read together, from the same OptionService, so the
// structure and its archive bases cannot come from different snapshots of the
// database. OptionService maps an absent option to the empty string, which is
// exactly what routing.Parse treats as WordPress's "plain" setting, so a fresh
// install with no permalink rows configured needs no special case here.
//
// It always returns a usable Structure and never terminates the process. When
// routing.Parse rejects the configured structure it returns a flat Structure
// alongside its error, and this function logs that at WARN and serves flat
// rather than refusing to boot: grimoire reads a database it does not own, and
// a structure it cannot parse should degrade to a working flat site rather than
// an outage (M9a Req 4.3).
//
// All three branches name both resolved archive bases, and every
// Structure.Notes() entry is logged after whichever branch ran (Req 4.4). The
// structure verdict is a switch rather than the three early returns it used to
// be, which is what lets one note report serve all three. routing.Parse resolves
// both bases -- and records their diagnostics -- before it can fail or decide the
// structure is flat: the error path returns a flat copy that already carries them
// (internal/routing/routing.go). The unsupported branch named neither base
// pre-M9b, and it is the branch where an operator most needs them, since the flat
// fallback keeps serving /{CategoryBase}/{slug} and /{TagBase}/{slug} while they
// fix the structure.
func resolvePermalinks(ctx context.Context, opts *content.OptionService, log *slog.Logger) routing.Structure {
	raw := opts.Get(ctx, content.OptionPermalinkStructure)
	st, err := routing.Parse(
		raw,
		opts.Get(ctx, content.OptionCategoryBase),
		opts.Get(ctx, content.OptionTagBase),
	)
	switch {
	case err != nil:
		// The message states the consequence, not only the cause: an operator
		// reading this one line must understand that the site's published URLs
		// are broken, which is what stops this condition being discovered as a
		// site-wide 404 instead (Req 4.4). The wrapped error names the
		// offending token(s), or the structure itself when no single token is
		// at fault (Req 4.2). The bases are named here too: the archives are
		// the surface that still works on this path, so which segment serves
		// them is exactly what an operator needs while the fallback is active.
		log.Warn("unsupported permalink_structure: falling back to flat /{slug} routes, "+
			"so published URLs will not resolve while the fallback is active",
			"structure", raw,
			"err", err,
			"supported_tokens", strings.Join(routing.SupportedTokens(), " "),
			"category_base", st.CategoryBase,
			"tag_base", st.TagBase,
		)
	case st.Flat:
		log.Info("permalinks: plain structure, serving flat /{slug} routes",
			"category_base", st.CategoryBase,
			"tag_base", st.TagBase,
		)
	default:
		log.Info("permalinks: resolved structure",
			"structure", st.Raw,
			"trailing_slash", st.TrailingSlash,
			"category_base", st.CategoryBase,
			"tag_base", st.TagBase,
		)
	}
	logPermalinkNotes(log, st)
	return st
}

// logPermalinkNotes logs every Structure.Notes() entry at WARN, one record each,
// alongside the structure line above (Req 4.4).
//
// WARN is the level because routing.Parse returns a nil error and a usable
// Structure for all of these: a collision is invisible on the request path, where
// it costs an entire archive surface -- or, for the front collision, would have
// cost every post URL on the site had the archive not been the surface that
// degrades. Nothing anywhere else says which reading won.
//
// The note is the attribute rather than the message, so a log pipeline can group
// these records and an operator can read the whole diagnostic. Each entry already
// names the option at fault, the value it resolved to and which of the two
// competing readings won, so it is logged as given; re-wording one here would
// only give an operator a second phrasing to reconcile with -check's report.
//
// Nothing is logged when nothing collides. A WARN seen on every boot is one
// operators stop reading, which would hide the collisions this exists to report
// -- the argument internal/routing's own TestNotesEmptyWhenNothingCollides makes
// one layer down, and the one migrate -check's report follows.
func logPermalinkNotes(log *slog.Logger, st routing.Structure) {
	for _, note := range st.Notes() {
		log.Warn("permalink option diagnostic", "note", note)
	}
}
