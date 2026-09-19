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
