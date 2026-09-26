package routing_test

import (
	"errors"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// WordPress's own permalink presets, which are the structures a real site is
// overwhelmingly likely to be using.
const (
	presetDayAndName   = "/%year%/%monthnum%/%day%/%postname%/"
	presetMonthAndName = "/%year%/%monthnum%/%postname%/"
	presetPostName     = "/%postname%/"
	presetNumeric      = "/archives/%post_id%"
)

func TestParsePresets(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		wantFlat     bool
		wantTrailing bool
		wantChiFirst string
		wantCatBase  string
		wantTagBase  string
	}{
		{
			name:         "day and name",
			structure:    presetDayAndName,
			wantTrailing: true,
			wantChiFirst: "/{year}/{monthnum}/{day}/{postname}",
			wantCatBase:  "category",
			wantTagBase:  "tag",
		},
		{
			name:         "month and name",
			structure:    presetMonthAndName,
			wantTrailing: true,
			wantChiFirst: "/{year}/{monthnum}/{postname}",
			wantCatBase:  "category",
			wantTagBase:  "tag",
		},
		{
			name:         "post name",
			structure:    presetPostName,
			wantTrailing: true,
			wantChiFirst: "/{postname}",
			wantCatBase:  "category",
			wantTagBase:  "tag",
		},
		{
			name:         "numeric with a literal segment",
			structure:    presetNumeric,
			wantTrailing: false,
			wantChiFirst: "/archives/{post_id}",
			wantCatBase:  "category",
			wantTagBase:  "tag",
		},
		{
			name:        "empty structure is flat",
			structure:   "",
			wantFlat:    true,
			wantCatBase: "category",
			wantTagBase: "tag",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tc.structure, err)
			}
			if s.Flat != tc.wantFlat {
				t.Errorf("Flat = %v, want %v", s.Flat, tc.wantFlat)
			}
			if s.TrailingSlash != tc.wantTrailing {
				t.Errorf("TrailingSlash = %v, want %v", s.TrailingSlash, tc.wantTrailing)
			}
			if s.CategoryBase != tc.wantCatBase {
				t.Errorf("CategoryBase = %q, want %q", s.CategoryBase, tc.wantCatBase)
			}
			if s.TagBase != tc.wantTagBase {
				t.Errorf("TagBase = %q, want %q", s.TagBase, tc.wantTagBase)
			}
			if s.Raw != tc.structure {
				t.Errorf("Raw = %q, want %q", s.Raw, tc.structure)
			}
			if tc.wantFlat {
				if got := s.ChiPatterns(); len(got) != 0 {
					t.Errorf("ChiPatterns() = %v, want none for a flat structure", got)
				}
				return
			}
			got := s.ChiPatterns()
			if len(got) == 0 || got[0] != tc.wantChiFirst {
				t.Errorf("ChiPatterns()[0] = %v, want %q", got, tc.wantChiFirst)
			}
		})
	}
}

// A trailing-slash and a non-trailing-slash form must BOTH be registrable, so
// the handler can 301 the non-canonical one instead of chi 404ing it before the
// handler ever runs.
func TestChiPatternsCoverBothSlashForms(t *testing.T) {
	s, err := routing.Parse(presetDayAndName, "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	pats := s.ChiPatterns()
	if len(pats) != 2 {
		t.Fatalf("ChiPatterns() = %v, want 2 (with and without trailing slash)", pats)
	}
	if pats[0] != "/{year}/{monthnum}/{day}/{postname}" {
		t.Errorf("pats[0] = %q", pats[0])
	}
	if pats[1] != "/{year}/{monthnum}/{day}/{postname}/" {
		t.Errorf("pats[1] = %q", pats[1])
	}
}

func TestParseBaseOverrides(t *testing.T) {
	s, err := routing.Parse(presetPostName, "sections", "topics")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.CategoryBase != "sections" {
		t.Errorf("CategoryBase = %q, want %q", s.CategoryBase, "sections")
	}
	if s.TagBase != "topics" {
		t.Errorf("TagBase = %q, want %q", s.TagBase, "topics")
	}
}

func TestParseUnsupported(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		wantIn    string // substring the error must name, so the operator can act
	}{
		{"category token", "/%category%/%postname%/", "%category%"},
		{"author token", "/%author%/%postname%/", "%author%"},
		{"unknown token", "/%nonsense%/%postname%/", "%nonsense%"},
		{"no identifying token", "/%year%/%monthnum%/", "identifying"},
		{"only literals", "/blog/", "identifying"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err == nil {
				t.Fatalf("Parse(%q) = nil error, want ErrUnsupported", tc.structure)
			}
			if !errors.Is(err, routing.ErrUnsupported) {
				t.Fatalf("error %v does not wrap ErrUnsupported", err)
			}
			if !contains(err.Error(), tc.wantIn) {
				t.Errorf("error %q does not mention %q", err.Error(), tc.wantIn)
			}
			// Requirement 4.3: the caller must be able to fall back and keep
			// serving, so Parse returns a usable flat Structure alongside the
			// error rather than a zero value.
			if !s.Flat {
				t.Errorf("returned Structure.Flat = false, want true so the caller can fall back")
			}
		})
	}
}

func TestMatchDigitWidths(t *testing.T) {
	s, err := routing.Parse(presetDayAndName, "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cases := []struct {
		name   string
		params map[string]string
		wantOK bool
	}{
		{"valid", map[string]string{"year": "2026", "monthnum": "07", "day": "02", "postname": "hello"}, true},
		{"one digit month rejected", map[string]string{"year": "2026", "monthnum": "7", "day": "02", "postname": "hello"}, false},
		{"one digit day rejected", map[string]string{"year": "2026", "monthnum": "07", "day": "2", "postname": "hello"}, false},
		{"two digit year rejected", map[string]string{"year": "26", "monthnum": "07", "day": "02", "postname": "hello"}, false},
		{"non numeric year rejected", map[string]string{"year": "abcd", "monthnum": "07", "day": "02", "postname": "hello"}, false},
		{"empty postname rejected", map[string]string{"year": "2026", "monthnum": "07", "day": "02", "postname": ""}, false},
		{"month out of range rejected", map[string]string{"year": "2026", "monthnum": "13", "day": "02", "postname": "hello"}, false},
		{"day out of range rejected", map[string]string{"year": "2026", "monthnum": "07", "day": "32", "postname": "hello"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := s.Match(tc.params)
			if ok != tc.wantOK {
				t.Fatalf("Match(%v) ok = %v, want %v", tc.params, ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if ref.Slug != "hello" || ref.Year != 2026 || ref.Month != 7 || ref.Day != 2 {
				t.Errorf("Ref = %+v, want slug=hello 2026-07-02", ref)
			}
		})
	}
}

func TestMatchPostID(t *testing.T) {
	s, err := routing.Parse(presetNumeric, "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	ref, ok := s.Match(map[string]string{"post_id": "400774"})
	if !ok {
		t.Fatal("Match rejected a valid post_id")
	}
	if ref.ID != 400774 {
		t.Errorf("Ref.ID = %d, want 400774", ref.ID)
	}
	if ref.Slug != "" {
		t.Errorf("Ref.Slug = %q, want empty for an id-based structure", ref.Slug)
	}
	if _, ok := s.Match(map[string]string{"post_id": "not-a-number"}); ok {
		t.Error("Match accepted a non-numeric post_id")
	}
	if _, ok := s.Match(map[string]string{"post_id": "0"}); ok {
		t.Error("Match accepted post_id 0, which is never a valid row id")
	}
}

func TestCanonical(t *testing.T) {
	// 2026-07-02. Deliberately a single-digit month and day so zero-padding is
	// actually exercised.
	post := domain.Post{
		ID:   400774,
		Slug: "hello-world",
		Date: time.Date(2026, 7, 2, 13, 45, 0, 0, time.UTC),
	}
	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{"day and name", presetDayAndName, "/2026/07/02/hello-world/"},
		{"month and name", presetMonthAndName, "/2026/07/hello-world/"},
		{"post name", presetPostName, "/hello-world/"},
		{"numeric with literal", presetNumeric, "/archives/400774"},
		{"no trailing slash in structure", "/%year%/%postname%", "/2026/hello-world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := s.Canonical(post); got != tc.want {
				t.Errorf("Canonical() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCanonicalFlatFallsBackToSlug(t *testing.T) {
	s, err := routing.Parse("", "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	post := domain.Post{ID: 7, Slug: "hello-world"}
	if got := s.Canonical(post); got != "/hello-world" {
		t.Errorf("Canonical() = %q, want %q for a flat structure", got, "/hello-world")
	}
}

// Requirement 3.5. A redirect loop here would be a self-inflicted denial of
// service on every post URL, so the fixed-point property is asserted directly:
// canonicalising a path that is already canonical must not change it.
func TestCanonicalIsFixedPoint(t *testing.T) {
	post := domain.Post{ID: 12, Slug: "some-post", Date: time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)}
	for _, structure := range []string{presetDayAndName, presetMonthAndName, presetPostName, presetNumeric} {
		s, err := routing.Parse(structure, "", "")
		if err != nil {
			t.Fatalf("Parse(%q): %v", structure, err)
		}
		once := s.Canonical(post)
		twice := s.Canonical(post)
		if once != twice {
			t.Errorf("%s: Canonical not deterministic: %q then %q", structure, once, twice)
		}
		// The canonical path must itself match the structure, otherwise the
		// handler would redirect it again.
		params, ok := paramsFromPath(s, once)
		if !ok {
			t.Fatalf("%s: canonical path %q does not match its own ChiPatterns shape", structure, once)
		}
		ref, ok := s.Match(params)
		if !ok {
			t.Fatalf("%s: Match rejected its own canonical path %q (params %v)", structure, once, params)
		}
		if ref.Slug != "" && ref.Slug != post.Slug {
			t.Errorf("%s: round-tripped slug = %q, want %q", structure, ref.Slug, post.Slug)
		}
		if ref.ID != 0 && ref.ID != post.ID {
			t.Errorf("%s: round-tripped id = %d, want %d", structure, ref.ID, post.ID)
		}
	}
}

// Canonical must use the post's stored local date, not a UTC-shifted one: a post
// published at 2026-01-01 00:30 local would otherwise canonicalise to the
// previous day and permanently redirect away from its real URL.
func TestCanonicalUsesStoredDateWithoutTimezoneShift(t *testing.T) {
	s, err := routing.Parse(presetDayAndName, "", "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// 00:30 in a +10 zone. Converting to UTC would move this to the previous day.
	zone := time.FixedZone("UTC+10", 10*60*60)
	post := domain.Post{ID: 1, Slug: "new-year", Date: time.Date(2026, 1, 1, 0, 30, 0, 0, zone)}
	if got, want := s.Canonical(post), "/2026/01/01/new-year/"; got != want {
		t.Errorf("Canonical() = %q, want %q (must not convert to UTC)", got, want)
	}
}

// paramsFromPath splits a canonical path back into chi-style params using the
// structure's own token order, so the fixed-point test does not need a router.
func paramsFromPath(s routing.Structure, path string) (map[string]string, bool) {
	return s.ParamsFromPath(path)
}

func contains(haystack, needle string) bool {
	return len(needle) == 0 || (len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

// Requirements 6.1, 6.3 and 6.4. A date archive's interval is the seat of the
// whole feature: get the granularity wrong and a month archive lists a year, get
// the zone wrong and every boundary silently shifts by the host's offset.
//
// The bounds are asserted as a half-open [start, end): the start instant is
// included and the end instant is the first one excluded, so a post published
// exactly on the upper bound belongs to the next archive and never to two.
func TestDateRefRange(t *testing.T) {
	utc := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	cases := []struct {
		name      string
		ref       routing.DateRef
		wantStart time.Time
		wantEnd   time.Time
	}{
		{
			name:      "year only spans the whole year",
			ref:       routing.DateRef{Year: 2024},
			wantStart: utc(2024, time.January, 1),
			wantEnd:   utc(2025, time.January, 1),
		},
		{
			name:      "year and month spans that month",
			ref:       routing.DateRef{Year: 2024, Month: 5},
			wantStart: utc(2024, time.May, 1),
			wantEnd:   utc(2024, time.June, 1),
		},
		{
			// December is the case an off-by-one month increment gets wrong: the
			// end rolls into the following year rather than to month 13.
			name:      "december rolls the end into the next year",
			ref:       routing.DateRef{Year: 2024, Month: 12},
			wantStart: utc(2024, time.December, 1),
			wantEnd:   utc(2025, time.January, 1),
		},
		{
			name:      "year month and day spans one day",
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 17},
			wantStart: utc(2024, time.May, 17),
			wantEnd:   utc(2024, time.May, 18),
		},
		{
			// Last day of a month: the end must roll into the next month.
			name:      "last day of a month rolls the end into the next month",
			ref:       routing.DateRef{Year: 2024, Month: 5, Day: 31},
			wantStart: utc(2024, time.May, 31),
			wantEnd:   utc(2024, time.June, 1),
		},
		{
			// A real leap day must be accepted, not rejected along with the
			// impossible dates below.
			name:      "real leap day is a valid date",
			ref:       routing.DateRef{Year: 2024, Month: 2, Day: 29},
			wantStart: utc(2024, time.February, 29),
			wantEnd:   utc(2024, time.March, 1),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start, end, ok := tc.ref.Range()
			if !ok {
				t.Fatalf("Range() ok = false for %+v, want true", tc.ref)
			}
			if !start.Equal(tc.wantStart) {
				t.Errorf("start = %s, want %s", start.Format(time.RFC3339), tc.wantStart.Format(time.RFC3339))
			}
			if !end.Equal(tc.wantEnd) {
				t.Errorf("end = %s, want %s", end.Format(time.RFC3339), tc.wantEnd.Format(time.RFC3339))
			}
			// Requirement 6.4: the interval carries no zone offset, so
			// wprepo's formatTS -- t.UTC().Format(tsLayout) -- is the identity
			// on both bounds. Built in time.Local instead, a May 2024 archive on
			// a UTC-7 host would query from 2024-05-01 07:00:00, dropping the
			// first seven hours of May 1. The offset is asserted as well as the
			// instant because an instant comparison alone passes on a host whose
			// local zone happens to be UTC, which is most CI runners.
			for label, got := range map[string]time.Time{"start": start, "end": end} {
				if _, offset := got.Zone(); offset != 0 {
					t.Errorf("%s zone offset = %d, want 0 so formatTS is the identity", label, offset)
				}
			}
		})
	}
}

// Requirement 6.3: components that do not form a real calendar date are rejected
// here, so the handler 404s without ever reaching a query.
func TestDateRefRangeRejectsImpossibleDates(t *testing.T) {
	cases := []struct {
		name string
		ref  routing.DateRef
	}{
		{"day past the end of the month", routing.DateRef{Year: 2024, Month: 2, Day: 30}},
		{"month out of range", routing.DateRef{Year: 2024, Month: 13, Day: 1}},
		{"leap day in a non-leap year", routing.DateRef{Year: 2023, Month: 2, Day: 29}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, ok := tc.ref.Range(); ok {
				t.Errorf("Range() ok = true for %+v, want false", tc.ref)
			}
		})
	}
}

// Requirements 4.7, 4.8 and 4.9. The front applies to category and tag archives
// only when the corresponding base option is *unset*, and "unset" is
// WordPress's truthiness test on get_option() -- not "differs from the default".
// So Parse has to record whether each base was **provided**, which is strictly
// more than what it resolved to: the empty case and the explicit-default case
// resolve to the same string and must still be distinguishable afterwards.
func TestParseRecordsBaseProvision(t *testing.T) {
	cases := []struct {
		name         string
		categoryBase string
		tagBase      string
		wantCatBase  string
		wantTagBase  string
		wantCatSet   bool
		wantTagSet   bool
	}{
		{
			// content.OptionService maps an absent option to the empty string,
			// so the empty string arriving here *is* a falsy get_option().
			name:        "neither option provided",
			wantCatBase: "category",
			wantTagBase: "tag",
		},
		{
			name:         "both options provided with non-default values",
			categoryBase: "sections",
			tagBase:      "topics",
			wantCatBase:  "sections",
			wantTagBase:  "topics",
			wantCatSet:   true,
			wantTagSet:   true,
		},
		{
			// The case that separates provision from inequality-with-the-default,
			// and the reason these flags exist at all. WordPress tests the
			// truthiness of get_option( 'category_base' ), so the string
			// "category" is set: create_initial_taxonomies() then registers the
			// taxonomy with with_front => false and the front is dropped. An
			// implementation that reported provision by comparing the resolved
			// base against DefaultCategoryBase passes both cases above and fails
			// this one, keeping a front WordPress would have dropped.
			name:         "both options provided as the default strings",
			categoryBase: routing.DefaultCategoryBase,
			tagBase:      routing.DefaultTagBase,
			wantCatBase:  "category",
			wantTagBase:  "tag",
			wantCatSet:   true,
			wantTagSet:   true,
		},
		{
			// The two flags are independent, because WordPress evaluates
			// with_front once per taxonomy: the same structure can carry the
			// front on its tag archive and not on its category archive.
			name:         "only the category base provided",
			categoryBase: "sections",
			wantCatBase:  "sections",
			wantTagBase:  "tag",
			wantCatSet:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(presetPostName, tc.categoryBase, tc.tagBase)
			if err != nil {
				t.Fatalf("Parse(%q, %q, %q) error: %v",
					presetPostName, tc.categoryBase, tc.tagBase, err)
			}
			if s.CategoryBase != tc.wantCatBase {
				t.Errorf("CategoryBase = %q, want %q", s.CategoryBase, tc.wantCatBase)
			}
			if s.TagBase != tc.wantTagBase {
				t.Errorf("TagBase = %q, want %q", s.TagBase, tc.wantTagBase)
			}
			if s.CategoryBaseSet != tc.wantCatSet {
				t.Errorf("CategoryBaseSet = %v, want %v (category_base = %q)",
					s.CategoryBaseSet, tc.wantCatSet, tc.categoryBase)
			}
			if s.TagBaseSet != tc.wantTagSet {
				t.Errorf("TagBaseSet = %v, want %v (tag_base = %q)",
					s.TagBaseSet, tc.wantTagSet, tc.tagBase)
			}
		})
	}
}
