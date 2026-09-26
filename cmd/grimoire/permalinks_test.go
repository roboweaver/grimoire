package main

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// WordPress permalink_structure values used below. The supported set mirrors
// the constants the routing and web tests use, so all three layers are
// asserted against the same structures.
const (
	mainStructDayAndName = "/%year%/%monthnum%/%day%/%postname%/"
	mainStructPostName   = "/%postname%/"

	// Unsupported: %category% and %author% each require resolving a term or
	// user during path matching, which is out of scope for M9a.
	mainStructCategoryAndName = "/%category%/%postname%/"
	mainStructAuthorAndName   = "/%author%/%postname%/"

	// Unsupported for a different reason: parseable tokens throughout, but
	// none of them identifies a single post (Req 2.4 feeding Req 4.1).
	mainStructDateOnly = "/%year%/%monthnum%/%day%/"
)

// TestResolvePermalinksUnsupportedStructureFallsBackLoudly is the Phase 6
// startup-level assertion for Requirement 4.
//
// internal/routing already covers routing.Parse returning ErrUnsupported and
// naming the offending token, so this test deliberately does not re-test the
// parser. What it covers is the behavior of the *startup path* built on top of
// it, which nothing asserts today:
//
//   - Req 4.2 — the flat fallback is actually applied, i.e. the Structure the
//     startup path hands to the server and the REST mapper is Flat, so the
//     /{slug} route stays canonical and no redirects are issued.
//   - Req 4.3 — startup still succeeds. The seam returns a usable Structure
//     rather than terminating the process: grimoire reads a database it does
//     not own, so an unparseable structure must degrade to a working flat site
//     rather than an outage.
//   - Req 4.1/4.2/4.4 — the operator is told, at WARN, which token(s) are
//     unsupported *and* that published URLs will not resolve while the
//     fallback is active. Cause without consequence is what makes this
//     condition get discovered as a site-wide 404 instead of at boot.
func TestResolvePermalinksUnsupportedStructureFallsBackLoudly(t *testing.T) {
	cases := []struct {
		name string
		// structure is the permalink_structure option value.
		structure string
		// wantNamed are substrings the WARN record must contain. For a
		// bad-token structure these are the tokens themselves; for the
		// no-identifier case there is no single offending token, so the
		// warning must at least name the structure it could not use.
		wantNamed []string
	}{
		{
			name:      "unsupported %category% token",
			structure: mainStructCategoryAndName,
			wantNamed: []string{"%category%"},
		},
		{
			name:      "unsupported %author% token",
			structure: mainStructAuthorAndName,
			wantNamed: []string{"%author%"},
		},
		{
			name:      "no identifying token",
			structure: mainStructDateOnly,
			wantNamed: []string{mainStructDateOnly},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, sink := newRecordingLogger()
			opts := content.NewOptionService(&fakeMainOptionRepo{values: map[string]string{
				"permalink_structure": tc.structure,
			}})

			// Req 4.3: this returns. A seam that called os.Exit or panicked
			// here would fail the test by killing the run rather than by
			// reporting, which is the point -- refusing to boot is the
			// behavior explicitly rejected.
			st := resolvePermalinks(context.Background(), opts, log)

			// Req 4.2: the fallback is the flat structure, not the
			// half-parsed one.
			if !st.Flat {
				t.Errorf("Structure.Flat = false, want true (unsupported structure must fall back to flat)")
			}
			// And it is usable, not merely flagged: the flat path is
			// canonical and no structure-derived route is registered.
			p := domain.Post{ID: 42, Slug: "hello-world", Date: time.Date(2024, 3, 5, 9, 30, 0, 0, time.UTC)}
			if got, want := st.Canonical(p), "/hello-world"; got != want {
				t.Errorf("Canonical = %q, want %q", got, want)
			}
			if got := st.ChiPatterns(); len(got) != 0 {
				t.Errorf("ChiPatterns = %v, want none while the fallback is active", got)
			}

			// Req 4.3: degrading is not an error condition. An ERROR record
			// here would read as a failed boot in an operator's log pipeline.
			if errs := sink.at(slog.LevelError); len(errs) != 0 {
				t.Errorf("got %d ERROR record(s), want 0: %v", len(errs), errs)
			}

			warns := sink.at(slog.LevelWarn)
			if len(warns) != 1 {
				t.Fatalf("got %d WARN record(s), want exactly 1: %v", len(warns), warns)
			}
			warn := warns[0]

			// Req 4.2: name the cause.
			for _, want := range tc.wantNamed {
				if !strings.Contains(warn, want) {
					t.Errorf("WARN record does not name %q: %s", want, warn)
				}
			}
			// Req 4.4: state the consequence. The phrasing is the
			// requirement's own -- an operator reading only this line must
			// understand that the site's published URLs are broken, not just
			// that a token was rejected.
			for _, want := range []string{"published URLs", "will not resolve"} {
				if !strings.Contains(warn, want) {
					t.Errorf("WARN record does not state the consequence %q: %s", want, warn)
				}
			}
		})
	}
}

// TestResolvePermalinksSupportedStructureIsServedQuietly is the control for the
// test above: without it, a seam that unconditionally returned the flat
// structure and warned every time would pass the fallback assertions while
// breaking every site that has permalinks configured correctly.
//
// It also covers Req 1.2's plain setting, which must not warn at all -- a
// warning there would fire on every correctly configured plain site.
func TestResolvePermalinksSupportedStructureIsServedQuietly(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		wantFlat  bool
	}{
		{name: "plain (empty) structure", structure: "", wantFlat: true},
		{name: "day and name", structure: mainStructDayAndName, wantFlat: false},
		{name: "post name", structure: mainStructPostName, wantFlat: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log, sink := newRecordingLogger()
			opts := content.NewOptionService(&fakeMainOptionRepo{values: map[string]string{
				"permalink_structure": tc.structure,
				"category_base":       "sections",
				"tag_base":            "topics",
			}})

			st := resolvePermalinks(context.Background(), opts, log)

			if st.Flat != tc.wantFlat {
				t.Errorf("Structure.Flat = %v, want %v", st.Flat, tc.wantFlat)
			}
			// Req 1.1/1.4/1.5: category_base and tag_base are read from the
			// same option set, not left at their defaults.
			if st.CategoryBase != "sections" {
				t.Errorf("CategoryBase = %q, want %q", st.CategoryBase, "sections")
			}
			if st.TagBase != "topics" {
				t.Errorf("TagBase = %q, want %q", st.TagBase, "topics")
			}
			if warns := sink.at(slog.LevelWarn); len(warns) != 0 {
				t.Errorf("got %d WARN record(s), want 0: %v", len(warns), warns)
			}
			if errs := sink.at(slog.LevelError); len(errs) != 0 {
				t.Errorf("got %d ERROR record(s), want 0: %v", len(errs), errs)
			}
		})
	}
}

// fakeMainOptionRepo is an in-memory domain.OptionRepository. The tests drive
// the real content.OptionService over it rather than faking the service, so the
// option names the startup path reads are part of what is asserted (Req 1.1).
type fakeMainOptionRepo struct{ values map[string]string }

func (f *fakeMainOptionRepo) Get(_ context.Context, name string) (string, error) {
	v, ok := f.values[name]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

// logSink collects emitted records so a test can assert on level and text
// together. A text handler writing to a buffer would support substring checks
// but could not tell which record carried which level, and "the WARN names the
// token" is exactly a per-record claim.
type logSink struct {
	mu      sync.Mutex
	records []sunkRecord
}

type sunkRecord struct {
	level slog.Level
	text  string
}

// at returns the flattened text of every record logged at exactly level.
func (s *logSink) at(level slog.Level) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.records {
		if r.level == level {
			out = append(out, r.text)
		}
	}
	return out
}

type recordingHandler struct {
	sink *logSink
	pre  string
}

// newRecordingLogger returns a logger that records everything, and the sink it
// records into.
func newRecordingLogger() (*slog.Logger, *logSink) {
	sink := &logSink{}
	return slog.New(&recordingHandler{sink: sink}), sink
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	b.WriteString(h.pre)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	h.sink.records = append(h.sink.records, sunkRecord{level: r.Level, text: b.String()})
	return nil
}

// WithAttrs preformats the attrs into the flattened text, so a seam that
// attaches the structure via log.With(...) rather than at the call site is
// still asserted against the same way.
func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := &recordingHandler{sink: h.sink, pre: h.pre}
	for _, a := range attrs {
		next.pre += fmt.Sprintf(" %s=%v", a.Key, a.Value)
	}
	return next
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// Structures and bases the base-and-notes assertions below run over. The
// front-colliding structure is WordPress's own "Numeric" preset, which is the
// collision an operator provokes by renaming category_base to the very segment
// their URLs already live under (Req 4.4b).
const (
	mainStructNumeric = "/archives/%post_id%"

	mainBaseSections = "sections"
	mainBaseTopics   = "topics"
	mainBaseArchives = "archives"
)

// TestResolvePermalinksNamesArchiveBasesInEveryBranch covers the first half of
// Requirement 4.4's startup reporting: the line reporting the structure verdict
// names both resolved archive bases, in all three branches.
//
// The three branches are the point. routing.Parse resolves CategoryBase and
// TagBase *before* it can fail or decide the structure is flat -- the error path
// returns a flat copy that already carries them -- so the bases are as real on a
// rejected structure as on a resolved one, and the flat fallback keeps serving
// /{CategoryBase}/{slug} either way. The unsupported branch named neither base
// before this milestone, which is the branch where an operator most needs them.
//
// Expected values come from routing.Parse rather than from literals, so the
// assertion tracks base normalization (Req 4.11) instead of duplicating it.
func TestResolvePermalinksNamesArchiveBasesInEveryBranch(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
		// wantLevel is the level the branch reports its verdict at: WARN for
		// the unsupported fallback, INFO for the two it can serve.
		wantLevel slog.Level
	}{
		{
			name: "resolved structure with overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructDayAndName,
				content.OptionCategoryBase:       mainBaseSections,
				content.OptionTagBase:            mainBaseTopics,
			},
			wantLevel: slog.LevelInfo,
		},
		{
			name: "resolved structure with default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructPostName,
			},
			wantLevel: slog.LevelInfo,
		},
		{
			// A plain-permalink site still serves its category and tag
			// archives at their bases, so the bases are as load-bearing here
			// as anywhere.
			name: "plain structure with overridden bases",
			options: map[string]string{
				content.OptionCategoryBase: mainBaseSections,
				content.OptionTagBase:      mainBaseTopics,
			},
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "plain structure with default bases",
			options:   map[string]string{},
			wantLevel: slog.LevelInfo,
		},
		{
			name: "unsupported structure with overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructCategoryAndName,
				content.OptionCategoryBase:       mainBaseSections,
				content.OptionTagBase:            mainBaseTopics,
			},
			wantLevel: slog.LevelWarn,
		},
		{
			name: "unsupported structure with default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructDateOnly,
			},
			wantLevel: slog.LevelWarn,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := parseLikeStartup(t, tc.options)
			// None of these configurations collides, so the branch's verdict
			// line is the only record at its level. A row that did collide
			// would add a WARN and make the count assertion below ambiguous.
			if notes := st.Notes(); len(notes) != 0 {
				t.Fatalf("test setup is wrong: this configuration is meant to be "+
					"clean but Notes() reports %q", notes)
			}

			log, sink := newRecordingLogger()
			resolvePermalinks(context.Background(), newMainOptions(tc.options), log)

			records := sink.at(tc.wantLevel)
			if len(records) != 1 {
				t.Fatalf("got %d %v record(s), want exactly 1: %v",
					len(records), tc.wantLevel, records)
			}
			// Both bases on the verdict line itself, not merely somewhere in
			// the log: an operator correlating a base with the structure it
			// applies to reads one line.
			for _, want := range []string{
				"category_base=" + st.CategoryBase,
				"tag_base=" + st.TagBase,
			} {
				if !strings.Contains(records[0], want) {
					t.Errorf("%v record does not name %q: %s", tc.wantLevel, want, records[0])
				}
			}
		})
	}
}

// TestResolvePermalinksLogsNotesInEveryBranch covers the second half of
// Requirement 4.4's startup reporting: every Structure.Notes() entry is logged at
// WARN, in all three branches.
//
// Notes() is the non-fatal diagnostic channel -- routing.Parse returns a nil error
// and a usable Structure for all of these -- so the startup log is the only place
// a running server reports a collision at all. baseOptionNotes is recorded on the
// flat fallback too, which is why a collision is asserted on a rejected structure
// and on a plain one rather than only on a resolved one.
//
// Entries are asserted verbatim: each already names the option, its resolved
// value and which of the two competing readings won, so nothing here expects the
// startup path to re-word one.
func TestResolvePermalinksLogsNotesInEveryBranch(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			// Req 4.4a: both bases on one segment. The segment keeps its
			// category meaning, so tag archives silently vanish.
			name: "resolved structure, category_base equals tag_base",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructDayAndName,
				content.OptionCategoryBase:       mainBaseSections,
				content.OptionTagBase:            mainBaseSections,
			},
		},
		{
			// Req 4.4b: the base collides with the structure's leading
			// literal. This note exists only on a non-flat structure, since a
			// flat one has no front for a base to collide with.
			name: "resolved structure, category_base collides with the front",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructNumeric,
				content.OptionCategoryBase:       mainBaseArchives,
			},
		},
		{
			name: "plain structure, category_base equals tag_base",
			options: map[string]string{
				content.OptionCategoryBase: mainBaseSections,
				content.OptionTagBase:      mainBaseSections,
			},
		},
		{
			name: "unsupported structure, tag_base equals author",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructCategoryAndName,
				content.OptionTagBase:            routing.AuthorBase,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := parseLikeStartup(t, tc.options)
			notes := st.Notes()
			if len(notes) == 0 {
				t.Fatalf("test setup is wrong: this configuration produces no "+
					"Notes() entries, so it asserts nothing (structure %q, "+
					"category_base %q, tag_base %q)",
					tc.options[content.OptionPermalinkStructure],
					tc.options[content.OptionCategoryBase],
					tc.options[content.OptionTagBase])
			}

			log, sink := newRecordingLogger()
			// Req 4.3 still holds with notes in play: a diagnostic does not
			// stop the seam returning a usable Structure.
			if got := resolvePermalinks(context.Background(), newMainOptions(tc.options), log); got.Raw != st.Raw {
				t.Errorf("Structure.Raw = %q, want %q", got.Raw, st.Raw)
			}

			warns := sink.at(slog.LevelWarn)
			for _, note := range notes {
				if !containsAny(warns, note) {
					t.Errorf("no WARN record carries the Notes() entry\n\t%s\ngot:\n\t%s",
						note, strings.Join(warns, "\n\t"))
				}
			}
			// A collision is not a failed boot: it costs an archive surface,
			// and an ERROR here would read as an outage in a log pipeline.
			if errs := sink.at(slog.LevelError); len(errs) != 0 {
				t.Errorf("got %d ERROR record(s), want 0: %v", len(errs), errs)
			}
		})
	}
}

// TestResolvePermalinksLogsNoNotesOnACleanConfiguration is what keeps the notes
// above worth reading. A WARN an operator sees on every boot is one they learn to
// skip, and then the real collisions are invisible -- the same argument routing's
// own TestNotesEmptyWhenNothingCollides makes one layer down, asserted here
// because startup is where a human actually reads them.
//
// It asserts on marker phrases rather than on record counts because the
// unsupported branch legitimately emits one WARN of its own, so "no WARN records"
// would be the wrong claim on those rows.
func TestResolvePermalinksLogsNoNotesOnACleanConfiguration(t *testing.T) {
	// The phrases every collision note carries: "unreachable" for the three
	// base collisions and the front collision, "more than one path segment"
	// for a multi-segment base (internal/routing/notes.go).
	noteMarkers := []string{"unreachable", "more than one path segment"}

	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			name: "resolved structure, distinct overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructDayAndName,
				content.OptionCategoryBase:       mainBaseSections,
				content.OptionTagBase:            mainBaseTopics,
			},
		},
		{
			// Bases explicitly set to their own defaults are "set" (Req 4.9)
			// but collide with nothing, so they are not a fault either.
			name: "resolved structure, bases set to the default strings",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructPostName,
				content.OptionCategoryBase:       routing.DefaultCategoryBase,
				content.OptionTagBase:            routing.DefaultTagBase,
			},
		},
		{
			name:    "plain structure, no bases configured",
			options: map[string]string{},
		},
		{
			// A front exists here and no base collides with it. A note keyed
			// on "this structure has a front" would show up as a failure on
			// this row.
			name: "resolved structure with a front, no collision",
			options: map[string]string{
				content.OptionPermalinkStructure: "/blog/%year%/%monthnum%/%postname%/",
				content.OptionCategoryBase:       mainBaseSections,
			},
		},
		{
			name: "unsupported structure, distinct default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: mainStructDateOnly,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := parseLikeStartup(t, tc.options)
			if notes := st.Notes(); len(notes) != 0 {
				t.Fatalf("test setup is wrong: this configuration is meant to be "+
					"clean but Notes() reports %q", notes)
			}

			log, sink := newRecordingLogger()
			resolvePermalinks(context.Background(), newMainOptions(tc.options), log)

			warns := sink.at(slog.LevelWarn)
			for _, marker := range noteMarkers {
				if containsAny(warns, marker) {
					t.Errorf("a WARN record carries the diagnostic marker %q on a "+
						"clean configuration -- a warning an operator sees every "+
						"boot is one they stop reading:\n\t%s",
						marker, strings.Join(warns, "\n\t"))
				}
			}
		})
	}
}

// parseLikeStartup parses the same three options resolvePermalinks reads, so the
// expectations are derived from routing.Parse rather than restated as literals.
// An error is not a failure here: the unsupported rows depend on it, and Parse
// returns a usable flat Structure alongside it.
func parseLikeStartup(t *testing.T, options map[string]string) routing.Structure {
	t.Helper()

	st, _ := routing.Parse(
		options[content.OptionPermalinkStructure],
		options[content.OptionCategoryBase],
		options[content.OptionTagBase],
	)
	return st
}

// newMainOptions drives the real content.OptionService over the fake repository,
// so the option names the startup path reads stay part of what is asserted.
func newMainOptions(values map[string]string) *content.OptionService {
	return content.NewOptionService(&fakeMainOptionRepo{values: values})
}

// containsAny reports whether any record contains want.
func containsAny(records []string, want string) bool {
	for _, r := range records {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}
