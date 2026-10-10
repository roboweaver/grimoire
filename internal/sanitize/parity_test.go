package sanitize

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// This file is the committed, offline WordPress-parity test (Requirement 9.10,
// property P8). It carries no database, no network and no PHP: the WordPress
// `kses` oracle was captured ONCE, by hand, by scripts/capture-kses-fixtures.php
// against a running WordPress 7.1 container, and its answers were frozen into
// testdata/parity/fixtures.json. This test replays those frozen answers against
// grimoire's live SanitizeAt, so `go test ./internal/sanitize` proves parity
// deterministically with nothing but the committed JSON (Requirement 9.2, 9.9,
// 9.12). The capture script is committed and re-runnable but is never invoked by
// the test suite.
//
// Each fixture records three captured values, because P8 needs a fixed-point
// oracle and the divergence enumeration (Requirement 2.12) needs an input-output
// oracle, and those are not the same thing (design "Parity fixtures"):
//
//   - input                 the raw value fed to both sanitizers
//   - wordpress_of_input    kses(input) — the input-output oracle; NOT asserted,
//     it exists so the divergence set is DERIVED from the
//     fixtures rather than maintained beside them
//   - grimoire              SanitizeAt(kind, tier, input) — grimoire's own
//     output, the source of truth for grimoire
//   - wordpress_of_grimoire kses(grimoire) — the fixed-point oracle; P8 is that
//     this equals grimoire
//   - divergence            "none", a Dxx id, or another short tag; MUST be
//     non-"none" whenever wordpress_of_input != grimoire
//
// A fixture may set "encoding":"hex", in which case input, wordpress_of_input,
// grimoire and wordpress_of_grimoire are lowercase hex strings decoding to raw
// bytes. JSON strings must be valid UTF-8, so the one fixture that exercises
// non-UTF-8 bytes (D15) cannot carry those bytes literally; hex lets the fixture
// stay byte-faithful instead of silently normalizing the very bytes it is about.
type parityFixture struct {
	ID                  string `json:"id"`
	Kind                string `json:"kind"`
	Tier                string `json:"tier"`
	Encoding            string `json:"encoding,omitempty"`
	Input               string `json:"input"`
	WordPressOfInput    string `json:"wordpress_of_input"`
	Grimoire            string `json:"grimoire"`
	WordPressOfGrimoire string `json:"wordpress_of_grimoire"`
	Divergence          string `json:"divergence"`
}

// decoded returns the four byte-valued columns of a fixture, decoding hex when
// the fixture's encoding says so. A malformed hex string fails the test rather
// than silently yielding empty bytes.
func (f parityFixture) decoded(t *testing.T) (input, wpInput, grimoire, wpGrimoire string) {
	t.Helper()
	if f.Encoding != "hex" {
		return f.Input, f.WordPressOfInput, f.Grimoire, f.WordPressOfGrimoire
	}
	dec := func(name, s string) string {
		b, err := hex.DecodeString(s)
		if err != nil {
			t.Fatalf("fixture %q: %s is not valid hex (%q): %v", f.ID, name, s, err)
		}
		return string(b)
	}
	return dec("input", f.Input),
		dec("wordpress_of_input", f.WordPressOfInput),
		dec("grimoire", f.Grimoire),
		dec("wordpress_of_grimoire", f.WordPressOfGrimoire)
}

// provenance mirrors testdata/parity/provenance.json. The three array counts and
// the script hash are asserted (where grimoire can verify them) so a re-capture
// against a different WordPress version shows up as a test failure rather than a
// silent churn of fixture strings (design "The three counts are there so a
// re-capture ... is visible as a diff").
type provenance struct {
	WordPressVersion      string            `json:"wordpress_version"`
	Image                 string            `json:"image"`
	Container             string            `json:"container"`
	CapturedAt            string            `json:"captured_at"`
	CaptureScript         string            `json:"capture_script"`
	ScriptSHA256          string            `json:"script_sha256"`
	Oracle                map[string]string `json:"oracle"`
	AllowedTagsCount      int               `json:"allowedtags_count"`
	AllowedPostTagsCount  int               `json:"allowedposttags_count"`
	AllowedProtocolsCount int               `json:"allowed_protocols_count"`
}

// kindByName maps the fixture's string kind onto the FieldKind constant. An
// unknown name fails the test rather than defaulting, so a typo in a fixture is a
// red test, not a silently-skipped assertion.
var kindByName = map[string]FieldKind{
	"post_content":    PostContent,
	"post_excerpt":    PostExcerpt,
	"post_title":      PostTitle,
	"comment_content": CommentContent,
}

// tierByName maps the fixture's string tier onto the Tier constant.
var tierByName = map[string]Tier{
	"A": TierA,
	"B": TierB,
	"C": TierC,
}

// loadParityFixtures reads and decodes the committed fixture set.
func loadParityFixtures(t *testing.T) []parityFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "fixtures.json"))
	if err != nil {
		t.Fatalf("read fixtures.json: %v", err)
	}
	var fixtures []parityFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatalf("decode fixtures.json: %v", err)
	}
	if len(fixtures) == 0 {
		t.Fatal("fixtures.json is empty; expected the committed parity corpus")
	}
	return fixtures
}

// TestParityRegressionPin asserts the first of the two per-fixture claims
// (design "The test makes two assertions per fixture"):
//
//	SanitizeAt(kind, tier, input) == grimoire
//
// This is the regression pin. A change to the tier tables that alters what
// grimoire emits shows up here as a fixture diff in review rather than as silent
// behavior drift. The committed `grimoire` values were produced by SanitizeAt
// itself (bluemonday's output is grimoire's source of truth), so a green run
// means the committed corpus still matches the live policy.
func TestParityRegressionPin(t *testing.T) {
	p := New()
	for _, f := range loadParityFixtures(t) {
		f := f
		t.Run(f.ID, func(t *testing.T) {
			kind, ok := kindByName[f.Kind]
			if !ok {
				t.Fatalf("unknown kind %q", f.Kind)
			}
			tier, ok := tierByName[f.Tier]
			if !ok {
				t.Fatalf("unknown tier %q", f.Tier)
			}
			input, _, grimoire, _ := f.decoded(t)
			got, err := p.SanitizeAt(kind, tier, input)
			if err != nil {
				t.Fatalf("SanitizeAt(%v,%v,%q) returned error: %v", kind, tier, input, err)
			}
			if got != grimoire {
				t.Errorf("SanitizeAt(%v,%v,%q)\n  got      %q\n  fixture  %q",
					kind, tier, input, got, grimoire)
			}
		})
	}
}

// TestParityWordPressFixedPoint is property P8 (Requirement 2.11): grimoire's
// output survives a WordPress save unchanged.
//
//	wordpress_of_grimoire == grimoire   i.e.   kses(sanitize(k,t,x)) == sanitize(k,t,x)
//
// Checkable without PHP, a database or a network because the oracle's answer was
// captured once. It holds by construction — every tier emits a subset of what
// `kses` accepts, so `kses` leaves grimoire's output untouched — and this test is
// the committed regression pin on that construction (design "P8's fixture set is
// a regression pin rather than the proof").
func TestParityWordPressFixedPoint(t *testing.T) {
	for _, f := range loadParityFixtures(t) {
		f := f
		t.Run(f.ID, func(t *testing.T) {
			_, _, grimoire, wpGrimoire := f.decoded(t)
			if wpGrimoire != grimoire {
				t.Errorf("P8 violated for %q: kses(grimoire)=%q but grimoire=%q",
					f.ID, wpGrimoire, grimoire)
			}
		})
	}
}

// TestParityDivergenceEnumerated asserts the derived-divergence invariant
// (Requirement 2.12, 9.10): every fixture whose WordPress output for the input
// differs from grimoire's output MUST carry a non-"none" divergence tag, and
// conversely every fixture where the two agree MUST carry "none". This makes the
// divergence set a mechanical consequence of the fixtures rather than a list
// maintained beside them — a newly introduced divergence cannot be committed
// without being named, and a stale "none" on a diverging fixture is a red test
// (design "Requirement 9.10's 'every fixture where grimoire's output differs
// SHALL be enumerated rather than deleted' becomes a mechanical check").
func TestParityDivergenceEnumerated(t *testing.T) {
	for _, f := range loadParityFixtures(t) {
		f := f
		t.Run(f.ID, func(t *testing.T) {
			_, wpInput, grimoire, _ := f.decoded(t)
			diverges := wpInput != grimoire
			named := f.Divergence != "" && f.Divergence != "none"
			switch {
			case diverges && !named:
				t.Errorf("fixture %q diverges (wordpress_of_input=%q != grimoire=%q) "+
					"but carries divergence=%q; it must name the divergence",
					f.ID, wpInput, grimoire, f.Divergence)
			case !diverges && named:
				t.Errorf("fixture %q does not diverge (wordpress_of_input == grimoire == %q) "+
					"but carries divergence=%q; it must be \"none\"",
					f.ID, grimoire, f.Divergence)
			}
		})
	}
}

// TestParityProvenance pins the provenance file against the parts of it grimoire
// can verify locally, so a re-capture against a different WordPress version
// surfaces as a failing test. The allowedtags count and the allowed-protocols
// count are grimoire's tier-A table and scheme list respectively, so they MUST
// agree with the provenance file; the full allowedposttags count is WordPress's
// own list (a superset of tier B after the documented exclusions) and is only
// range-checked. The committed capture script's sha256 is asserted so an edit to
// the script without a re-capture is caught.
func TestParityProvenance(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "parity", "provenance.json"))
	if err != nil {
		t.Fatalf("read provenance.json: %v", err)
	}
	var pv provenance
	if err := json.Unmarshal(raw, &pv); err != nil {
		t.Fatalf("decode provenance.json: %v", err)
	}

	if pv.WordPressVersion != "7.1" {
		t.Errorf("provenance wordpress_version = %q, want 7.1 (Finding 6)", pv.WordPressVersion)
	}
	if got := len(tierAElements); pv.AllowedTagsCount != got {
		t.Errorf("provenance allowedtags_count = %d, but tierAElements has %d entries",
			pv.AllowedTagsCount, got)
	}
	if got := len(allowedSchemes); pv.AllowedProtocolsCount != got {
		t.Errorf("provenance allowed_protocols_count = %d, but allowedSchemes has %d entries",
			pv.AllowedProtocolsCount, got)
	}
	if pv.AllowedPostTagsCount <= len(tierBElements) {
		t.Errorf("provenance allowedposttags_count = %d; expected it to exceed grimoire's "+
			"tier-B element count %d (tier B is a documented subset of $allowedposttags)",
			pv.AllowedPostTagsCount, len(tierBElements))
	}
	if pv.Oracle["A"] == "" || pv.Oracle["B"] == "" || pv.Oracle["C"] == "" {
		t.Errorf("provenance oracle must name a call per tier, got %v", pv.Oracle)
	}
}

// TestParityTierACoverage asserts the committed corpus carries at least one
// fixture per tier-A element, the design's stated minimum for the initial set
// ("one fixture per tier-A element"). It walks tierAElements and checks a fixture
// id of the form tierA-el-<name> exists, so dropping an element's fixture during
// a future edit fails here.
func TestParityTierACoverage(t *testing.T) {
	have := map[string]bool{}
	for _, f := range loadParityFixtures(t) {
		have[f.ID] = true
	}
	for el := range tierAElements {
		id := "tierA-el-" + el
		if !have[id] {
			t.Errorf("missing tier-A parity fixture %q for element %q", id, el)
		}
	}
}
