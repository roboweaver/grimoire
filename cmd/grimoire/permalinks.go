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
func resolvePermalinks(ctx context.Context, opts *content.OptionService, log *slog.Logger) routing.Structure {
	raw := opts.Get(ctx, content.OptionPermalinkStructure)
	st, err := routing.Parse(
		raw,
		opts.Get(ctx, content.OptionCategoryBase),
		opts.Get(ctx, content.OptionTagBase),
	)
	if err != nil {
		// The message states the consequence, not only the cause: an operator
		// reading this one line must understand that the site's published URLs
		// are broken, which is what stops this condition being discovered as a
		// site-wide 404 instead (Req 4.4). The wrapped error names the
		// offending token(s), or the structure itself when no single token is
		// at fault (Req 4.2).
		log.Warn("unsupported permalink_structure: falling back to flat /{slug} routes, "+
			"so published URLs will not resolve while the fallback is active",
			"structure", raw,
			"err", err,
			"supported_tokens", strings.Join(routing.SupportedTokens(), " "),
		)
		return st
	}
	if st.Flat {
		log.Info("permalinks: plain structure, serving flat /{slug} routes",
			"category_base", st.CategoryBase,
			"tag_base", st.TagBase,
		)
		return st
	}
	log.Info("permalinks: resolved structure",
		"structure", st.Raw,
		"trailing_slash", st.TrailingSlash,
		"category_base", st.CategoryBase,
		"tag_base", st.TagBase,
	)
	return st
}
