package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/routing"
)

// reportPermalinks writes the resolved permalink structure and whether grimoire
// can serve it, as part of "migrate -check"'s preflight report (M9a Req 4.5).
//
// It is strictly read-only: three option reads and a pure parse. Nothing here
// influences any migration path, and -check still writes nothing to the
// database.
//
// The point of reporting it here is discoverability. Without this, an
// unsupported structure is only announced by the server's startup WARN, which
// an operator adopting a database typically reads after the site is already
// serving 404s. -check is the surface they run first.
//
// The options are read with the same content.Option* names and through the same
// routing.Parse call the server's startup path uses, so the two cannot disagree
// about what "supported" means for a given database.
//
// The resolved category_base and tag_base are deliberately not reported.
// routing.Parse resolves both, but no route honors either one yet: the category
// archive is still registered at the literal "/category/{slug}" and tag archives
// are not served at all until the follow-on spec implements roadmap group 9.C.
// Printing a resolved base would tell an operator their override is in effect
// when it is not, which is worse than saying nothing. Requirement 4.5 asks for
// the structure and whether it is supported; that is what this reports.
func reportPermalinks(ctx context.Context, w io.Writer, opts *content.OptionService) {
	raw := opts.Get(ctx, content.OptionPermalinkStructure)
	st, err := routing.Parse(
		raw,
		opts.Get(ctx, content.OptionCategoryBase),
		opts.Get(ctx, content.OptionTagBase),
	)
	if err != nil {
		// State the consequence alongside the cause, matching the startup
		// warning: knowing a token was rejected is not the same as knowing
		// every published URL is about to 404 (Req 4.2, 4.4).
		fmt.Fprintf(w, "Permalink structure %q is NOT supported.\n", raw)
		fmt.Fprintf(w, "  %v\n", err)
		fmt.Fprintf(w, "  grimoire will fall back to the flat /{slug} route, "+
			"so published URLs will not resolve. Supported tokens: %s\n",
			strings.Join(routing.SupportedTokens(), " "))
		return
	}
	if st.Flat {
		fmt.Fprintf(w, "Permalink structure is plain (%s is empty), which is supported; "+
			"posts serve at the flat /{slug} route.\n", content.OptionPermalinkStructure)
		return
	}
	// ChiPatterns returns the no-slash form then the slash form, and
	// TrailingSlash decides which of the two is canonical. Reporting the other
	// one would name a path that 301s.
	patterns := st.ChiPatterns()
	canonical := patterns[0]
	if st.TrailingSlash {
		canonical = patterns[1]
	}
	fmt.Fprintf(w, "Permalink structure %q is supported; posts serve at %s.\n",
		st.Raw, canonical)
}
