package routing_test

import (
	"slices"
	"testing"

	"github.com/roboweaver/grimoire/internal/routing"
)

// archiveRoundTrip is one archive kind's leg of the fixed-point property: build a
// canonical path from a known entity, classify it, and rebuild the path from what
// the classifier recovered.
type archiveRoundTrip struct {
	name string
	want routing.Kind
	// build calls the constructor under test with a fixed entity.
	build func(routing.Structure) string
	// rebuild calls the same constructor with the *classified* Target, so the
	// comparison is against a path the handler would actually redirect to.
	rebuild func(routing.Structure, routing.Target) string
	// payload asserts Classify recovered the entity the constructor was given.
	// Without it the round trip could pass while naming a different archive --
	// two constructors agreeing on a string says nothing about what it means.
	payload func(*testing.T, routing.Target)
}

// Requirements 2.4, 4.7 and 4.8, and design.md's "Redirect loops" note. Every
// archive kind's canonical path must be a fixed point of
// construct -> Classify -> construct:
//
//	Path(entity) == Path(Classify(Path(entity)))
//
// This is the no-redirect-loop property, asserted on the pure grammar rather
// than discovered as a 301 cycle in the handlers. Each archive handler resolves
// the entity, computes its canonical path and 301s when the request path differs
// (design.md's status-code table), so a canonical path that does not classify
// back to its own entity redirects to itself forever -- a self-inflicted denial
// of service on every URL of that kind, exactly the failure
// TestCanonicalIsFixedPoint rules out for posts.
//
// Two things make this worth asserting per kind rather than trusting by
// construction:
//
//   - **The nested category is derived through a graph walk**, not a field read.
//     The handler walks Target.Segments through the taxonomy graph and builds the
//     redirect target from the ancestry that walk returns, so a second pass that
//     recovered different segments would loop on every category URL. The table
//     therefore includes a three-level ancestry: a two-level case passes for an
//     implementation that recovers only the last parent, and a one-level case
//     passes for one that recovers nothing at all.
//
//   - **The front applies per kind** (Req 4.7, 4.8). archivePrefix is the single
//     place that asymmetry is decided and both the constructors and Classify's
//     per-row expected prefix read it, so the whole table runs against a
//     front-carrying structure with the taxonomy bases set and again with them
//     unset. A front applied on construction but not on classification -- or the
//     reverse -- shows up here as a failing round trip instead of as a redirect
//     loop in Phase 7, and the explicitly-set-to-the-default row catches the
//     implementation that decides "set" by comparing against
//     DefaultCategoryBase.
//
// The path is classified twice and rebuilt from both passes, because
// determinism across passes is the property the handler depends on, not
// determinism within one.
func TestArchivePathsAreClassifyFixedPoints(t *testing.T) {
	// The three-level ancestry the category legs use. Root first, ending in the
	// category's own slug.
	ancestry := []string{"news", "local", "towns"}

	kinds := []archiveRoundTrip{
		{
			// The zero-ancestor case, whose path is byte-for-byte today's flat
			// /category/{slug} and which Req 2.4 names explicitly.
			name: "top level category",
			want: routing.KindCategory,
			build: func(s routing.Structure) string {
				return s.CategoryPath([]string{"news"})
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.CategoryPath(tgt.Segments)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if want := []string{"news"}; !slices.Equal(tgt.Segments, want) {
					t.Errorf("Segments = %q, want %q", tgt.Segments, want)
				}
			},
		},
		{
			name: "three level nested category",
			want: routing.KindCategory,
			build: func(s routing.Structure) string {
				return s.CategoryPath(ancestry)
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.CategoryPath(tgt.Segments)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if !slices.Equal(tgt.Segments, ancestry) {
					t.Errorf("Segments = %q, want %q -- the walk's input must survive "+
						"the round trip in full, or the handler's second pass resolves "+
						"a different category", tgt.Segments, ancestry)
				}
			},
		},
		{
			name: "tag",
			want: routing.KindTag,
			build: func(s routing.Structure) string {
				return s.TagPath("go")
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.TagPath(tgt.Slug)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if tgt.Slug != "go" {
					t.Errorf("Slug = %q, want %q", tgt.Slug, "go")
				}
			},
		},
		{
			name: "author",
			want: routing.KindAuthor,
			build: func(s routing.Structure) string {
				return s.AuthorPath("alice")
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.AuthorPath(tgt.Slug)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if tgt.Slug != "alice" {
					t.Errorf("Slug = %q, want %q", tgt.Slug, "alice")
				}
			},
		},
		{
			name: "year archive",
			want: routing.KindDate,
			build: func(s routing.Structure) string {
				return s.DatePath(routing.DateRef{Year: 2024})
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.DatePath(tgt.Date)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if want := (routing.DateRef{Year: 2024}); tgt.Date != want {
					t.Errorf("Date = %+v, want %+v", tgt.Date, want)
				}
			},
		},
		{
			// Granularity has to survive the trip: a month archive whose Target
			// came back as a year would redirect /2024/05 to /2024 and list the
			// wrong range at the other end.
			name: "year month archive",
			want: routing.KindDate,
			build: func(s routing.Structure) string {
				return s.DatePath(routing.DateRef{Year: 2024, Month: 5})
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.DatePath(tgt.Date)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if want := (routing.DateRef{Year: 2024, Month: 5}); tgt.Date != want {
					t.Errorf("Date = %+v, want %+v", tgt.Date, want)
				}
			},
		},
		{
			name: "year month day archive",
			want: routing.KindDate,
			build: func(s routing.Structure) string {
				return s.DatePath(routing.DateRef{Year: 2024, Month: 5, Day: 17})
			},
			rebuild: func(s routing.Structure, tgt routing.Target) string {
				return s.DatePath(tgt.Date)
			},
			payload: func(t *testing.T, tgt routing.Target) {
				if want := (routing.DateRef{Year: 2024, Month: 5, Day: 17}); tgt.Date != want {
					t.Errorf("Date = %+v, want %+v", tgt.Date, want)
				}
			},
		},
	}

	configs := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
	}{
		{
			// Plain permalinks: no front, no trailing slash, the shapes grimoire
			// serves today. The date legs are skipped here -- a Flat structure
			// serves no date archive at all (Req 6.8).
			name: "flat structure",
		},
		{
			name:      "no front, default bases",
			structure: presetPostName,
		},
		{
			// Front applies to all four kinds.
			name:      "front with both bases unset",
			structure: presetFrontMonthAndName,
		},
		{
			// Front applies to date and author only. An implementation that
			// stripped the front once, before the per-kind rows, classifies
			// /sections/news/local/towns as nothing here while CategoryPath
			// keeps advertising it.
			name:         "front with both bases set",
			structure:    presetFrontMonthAndName,
			categoryBase: "sections",
			tagBase:      "topics",
		},
		{
			// Req 4.9: WordPress tests get_option()'s truthiness, so a base set
			// to the default string is *set* and drops the front. Construction
			// and classification have to agree about that, not just about the
			// resolved string.
			name:         "front with bases set to the default strings",
			structure:    presetFrontMonthAndName,
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
		},
		{
			// WordPress's Numeric preset: a literal front, no trailing slash,
			// and %post_id% at token 1, so its date legs round-trip through the
			// "date/" disambiguation segment as well as the front (Req 6.7).
			name:      "numeric preset with the date disambiguation prefix",
			structure: presetNumeric,
		},
	}

	for _, cfg := range configs {
		for _, k := range kinds {
			t.Run(cfg.name+"/"+k.name, func(t *testing.T) {
				s, err := routing.Parse(cfg.structure, cfg.categoryBase, cfg.tagBase)
				if err != nil {
					t.Fatalf("Parse(%q, %q, %q) error: %v",
						cfg.structure, cfg.categoryBase, cfg.tagBase, err)
				}

				first := k.build(s)
				if first == "" {
					if k.want == routing.KindDate && s.Flat {
						// Req 6.8: no date archive exists under plain
						// permalinks, so there is no path to round-trip.
						return
					}
					t.Fatalf("constructor returned no path for %s under %q",
						k.name, cfg.structure)
				}

				tgt := s.Classify(first)
				if tgt.Kind != k.want {
					t.Fatalf("Classify(%q) Kind = %v, want %v -- a canonical path "+
						"its own classifier does not recognise 404s or redirects away "+
						"from itself", first, tgt.Kind, k.want)
				}
				k.payload(t, tgt)
				if tgt.TrailingSlash != s.TrailingSlash {
					// The handler canonicalises the slash form from this field,
					// so a canonical path reported as the non-canonical form
					// redirects to itself.
					t.Errorf("Classify(%q) TrailingSlash = %v, want %v",
						first, tgt.TrailingSlash, s.TrailingSlash)
				}

				second := k.rebuild(s, tgt)
				if second != first {
					t.Fatalf("round trip changed the path: built %q, rebuilt %q "+
						"from Classify -- the handler would 301 %q to %q forever",
						first, second, first, second)
				}

				// The second pass is the one the loop depends on: the handler
				// classifies the redirect target again on the next request.
				again := s.Classify(second)
				if again.Kind != k.want {
					t.Fatalf("second pass: Classify(%q) Kind = %v, want %v",
						second, again.Kind, k.want)
				}
				k.payload(t, again)
				if third := k.rebuild(s, again); third != first {
					t.Errorf("second pass rebuilt %q, want %q", third, first)
				}
			})
		}
	}
}
