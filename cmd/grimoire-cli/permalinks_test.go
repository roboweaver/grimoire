package main

import (
	"bytes"
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// permalink_structure values exercised below. The supported set is the three
// WordPress presets; the unsupported set is one structure per reason a
// structure can be rejected (an out-of-scope token, and no identifying token
// at all).
const (
	cliStructDayAndName   = "/%year%/%monthnum%/%day%/%postname%/"
	cliStructMonthAndName = "/%year%/%monthnum%/%postname%/"
	cliStructPostName     = "/%postname%/"

	cliStructCategoryAndName = "/%category%/%postname%/"
	cliStructDateOnly        = "/%year%/%monthnum%/%day%/"
)

// TestReportPermalinksNamesStructureAndSupport covers Requirement 4.5: the
// -check report must name the resolved permalink structure and say whether it
// is supported, so an operator can discover an unusable structure before
// starting the server rather than as a site-wide 404 afterwards.
//
// The report is driven through the real content.OptionService over a fake
// repository, so the option names -check reads are part of what is asserted --
// a report that read "permalinks" instead of "permalink_structure" would say
// "plain" about every site on earth and pass any test that faked the service.
func TestReportPermalinksNamesStructureAndSupport(t *testing.T) {
	cases := []struct {
		name string
		// options seeds the {prefix}options rows -check would read.
		options map[string]string
		// wantContains are substrings the report must contain: the resolved
		// structure, and the verdict on it.
		wantContains []string
		// wantAbsent guards the verdict against being reported both ways --
		// "NOT supported" contains "supported", so a naive substring check
		// would pass on the wrong answer.
		wantAbsent []string
	}{
		{
			name:         "supported day and name",
			options:      map[string]string{content.OptionPermalinkStructure: cliStructDayAndName},
			wantContains: []string{cliStructDayAndName, "is supported"},
			wantAbsent:   []string{"NOT supported", "will not resolve"},
		},
		{
			name:         "supported month and name",
			options:      map[string]string{content.OptionPermalinkStructure: cliStructMonthAndName},
			wantContains: []string{cliStructMonthAndName, "is supported"},
			wantAbsent:   []string{"NOT supported", "will not resolve"},
		},
		{
			name:         "supported post name",
			options:      map[string]string{content.OptionPermalinkStructure: cliStructPostName},
			wantContains: []string{cliStructPostName, "is supported"},
			wantAbsent:   []string{"NOT supported", "will not resolve"},
		},
		{
			// Req 1.2: an absent permalink_structure row is WordPress's
			// "plain" setting, which is supported -- the flat route is
			// canonical. Reporting it as unsupported would send operators
			// chasing a non-problem on every fresh install.
			name:         "plain structure is reported as supported",
			options:      map[string]string{},
			wantContains: []string{"plain", "/{slug}"},
			wantAbsent:   []string{"NOT supported", "will not resolve"},
		},
		{
			name:    "unsupported %category% token",
			options: map[string]string{content.OptionPermalinkStructure: cliStructCategoryAndName},
			wantContains: []string{
				cliStructCategoryAndName,
				"NOT supported",
				"%category%",
				// Req 4.4's consequence, restated here because -check is
				// the surface an operator reads *before* the WARN exists.
				"will not resolve",
			},
		},
		{
			name:    "unsupported structure with no identifying token",
			options: map[string]string{content.OptionPermalinkStructure: cliStructDateOnly},
			wantContains: []string{
				cliStructDateOnly,
				"NOT supported",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			opts := content.NewOptionService(&fakeCLIOptionRepo{values: tc.options})

			reportPermalinks(context.Background(), &buf, opts)

			got := buf.String()
			if got == "" {
				t.Fatal("report is empty; -check must say something about permalinks")
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(got, want) {
					t.Errorf("report does not contain %q:\n%s", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("report unexpectedly contains %q:\n%s", absent, got)
				}
			}
		})
	}
}

// M9b replaces the pre-milestone TestReportPermalinksOmitsTheArchiveBases,
// which asserted the opposite of Requirement 4.6: it pinned -check's silence
// about category_base and tag_base on the grounds that no route honored either
// one. This milestone makes both bases the seat of chi registration, of
// Classify's segment comparison and of every archive link, so the silence is now
// the misinformation -- an operator who renamed their base has no other
// read-only surface telling them which segment is actually being served.
//
// Structures whose bases are worth reporting. The front-colliding case is
// WordPress's own "Numeric" preset, which is the collision an operator provokes
// by renaming category_base to the very segment their URLs already live under.
const (
	cliStructNumeric = "/archives/%post_id%"

	cliBaseSections = "sections"
	cliBaseTopics   = "topics"
	cliBaseArchives = "archives"
)

// TestReportPermalinksNamesArchiveBases covers Requirement 4.6: the -check
// report names the resolved category_base and tag_base.
//
// The table's three branches are the whole point. reportPermalinks returns early
// in the unsupported branch (permalinks.go:52) and again in the Flat branch
// (:57), while routing.Parse resolves both bases *before* either exit -- the
// error path returns a flat copy that already carries them
// (internal/routing/routing.go:111-123). An implementation that appends the
// bases after the last branch therefore reports nothing for the two
// configurations an operator is most likely to have: a plain-permalink site, and
// a site whose structure grimoire cannot parse. Both of those still serve
// /{CategoryBase}/{slug}, so both still need the base named.
//
// The expected values come from routing.Parse rather than from literals, so the
// assertion tracks base normalization (Req 4.11) instead of duplicating it.
func TestReportPermalinksNamesArchiveBases(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			name: "resolved structure with overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructDayAndName,
				content.OptionCategoryBase:       cliBaseSections,
				content.OptionTagBase:            cliBaseTopics,
			},
		},
		{
			name: "resolved structure with default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructPostName,
			},
		},
		{
			// The Flat branch returns at :57. A plain-permalink site still
			// serves its category and tag archives at their bases, so the
			// bases are as load-bearing here as anywhere.
			name: "plain structure with overridden bases",
			options: map[string]string{
				content.OptionCategoryBase: cliBaseSections,
				content.OptionTagBase:      cliBaseTopics,
			},
		},
		{
			name:    "plain structure with default bases",
			options: map[string]string{},
		},
		{
			// The unsupported branch returns at :52 -- and is the branch where
			// an operator most needs the bases, since the flat fallback is what
			// will be serving while they fix the structure.
			name: "unsupported structure with overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructCategoryAndName,
				content.OptionCategoryBase:       cliBaseSections,
				content.OptionTagBase:            cliBaseTopics,
			},
		},
		{
			name: "unsupported structure with default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructDateOnly,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := routing.Parse(
				tc.options[content.OptionPermalinkStructure],
				tc.options[content.OptionCategoryBase],
				tc.options[content.OptionTagBase],
			)

			got := runReportPermalinks(t, tc.options)

			assertNamesBase(t, got, content.OptionCategoryBase, st.CategoryBase)
			assertNamesBase(t, got, content.OptionTagBase, st.TagBase)
		})
	}
}

// TestReportPermalinksPrintsNotes covers the second half of Requirement 4.6:
// every Structure.Notes() entry appears in the report when a base collides.
//
// Notes() is the non-fatal diagnostic channel (Req 4.5) -- routing.Parse returns
// a nil error and a usable Structure for all of these -- so -check is the only
// read-only surface where a collision is visible before the site is serving.
// Each entry already names the option, its resolved value and which of the two
// competing readings won, so the report prints it as given; nothing here expects
// -check to re-word or re-wrap a note.
//
// Same three branches, for the same reason: baseOptionNotes is recorded on the
// flat fallback too (routing.go:177), so a collision is just as real on a
// plain-permalink site and on one whose structure was rejected.
func TestReportPermalinksPrintsNotes(t *testing.T) {
	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			// Req 4.4a: both bases on one segment. The segment keeps its
			// category meaning, so tag archives silently vanish.
			name: "resolved structure, category_base equals tag_base",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructDayAndName,
				content.OptionCategoryBase:       cliBaseSections,
				content.OptionTagBase:            cliBaseSections,
			},
		},
		{
			// Req 4.4a: a base on the author archive's literal base.
			name: "resolved structure, tag_base equals author",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructPostName,
				content.OptionTagBase:            routing.AuthorBase,
			},
		},
		{
			// Req 4.4b: the base collides with the structure's leading
			// literal. This note exists only on a non-flat structure, since a
			// flat one has no front, which is why it is here and not below.
			name: "resolved structure, category_base collides with the front",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructNumeric,
				content.OptionCategoryBase:       cliBaseArchives,
			},
		},
		{
			name: "plain structure, category_base equals tag_base",
			options: map[string]string{
				content.OptionCategoryBase: cliBaseSections,
				content.OptionTagBase:      cliBaseSections,
			},
		},
		{
			name: "unsupported structure, category_base equals author",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructCategoryAndName,
				content.OptionCategoryBase:       routing.AuthorBase,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := routing.Parse(
				tc.options[content.OptionPermalinkStructure],
				tc.options[content.OptionCategoryBase],
				tc.options[content.OptionTagBase],
			)
			notes := st.Notes()
			if len(notes) == 0 {
				t.Fatalf("test setup is wrong: this configuration produces no "+
					"Notes() entries, so it asserts nothing (structure %q, "+
					"category_base %q, tag_base %q)",
					tc.options[content.OptionPermalinkStructure],
					tc.options[content.OptionCategoryBase],
					tc.options[content.OptionTagBase])
			}

			got := runReportPermalinks(t, tc.options)

			for _, note := range notes {
				if !strings.Contains(got, note) {
					t.Errorf("report omits the Notes() entry\n\t%s\ngot:\n%s", note, got)
				}
			}
		})
	}
}

// TestReportPermalinksPrintsNoNotesOnACleanConfiguration is what keeps the
// report worth reading. A collision line an operator sees on every run is one
// they learn to skip, and then the real collisions above are invisible -- the
// same argument routing's own TestNotesEmptyWhenNothingCollides makes one layer
// down, asserted here because -check is where a human actually reads them.
//
// The marker phrases are the ones every collision note carries: "unreachable"
// for the three base collisions and the front collision, and "more than one path
// segment" for a multi-segment base (internal/routing/notes.go). None of these
// configurations has a fault, so none of the markers belongs in the output.
func TestReportPermalinksPrintsNoNotesOnACleanConfiguration(t *testing.T) {
	noteMarkers := []string{"unreachable", "more than one path segment"}

	cases := []struct {
		name    string
		options map[string]string
	}{
		{
			name: "resolved structure, distinct overridden bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructDayAndName,
				content.OptionCategoryBase:       cliBaseSections,
				content.OptionTagBase:            cliBaseTopics,
			},
		},
		{
			// Bases explicitly set to their own defaults are "set" (Req 4.9)
			// but collide with nothing, so they are not a fault either.
			name: "resolved structure, bases set to the default strings",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructPostName,
				content.OptionCategoryBase:       routing.DefaultCategoryBase,
				content.OptionTagBase:            routing.DefaultTagBase,
			},
		},
		{
			name:    "plain structure, no bases configured",
			options: map[string]string{},
		},
		{
			// A front exists here and no base collides with it. A note keyed on
			// "this structure has a front" rather than on the collision would
			// show up as a failure on this row.
			name: "resolved structure with a front, no collision",
			options: map[string]string{
				content.OptionPermalinkStructure: "/blog/%year%/%monthnum%/%postname%/",
				content.OptionCategoryBase:       cliBaseSections,
			},
		},
		{
			name: "unsupported structure, distinct default bases",
			options: map[string]string{
				content.OptionPermalinkStructure: cliStructDateOnly,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _ := routing.Parse(
				tc.options[content.OptionPermalinkStructure],
				tc.options[content.OptionCategoryBase],
				tc.options[content.OptionTagBase],
			)
			if notes := st.Notes(); len(notes) != 0 {
				t.Fatalf("test setup is wrong: this configuration is meant to be "+
					"clean but Notes() reports %q", notes)
			}

			got := runReportPermalinks(t, tc.options)

			for _, marker := range noteMarkers {
				if strings.Contains(got, marker) {
					t.Errorf("report contains the diagnostic marker %q on a clean "+
						"configuration -- a note an operator sees every run is one "+
						"they stop reading:\n%s", marker, got)
				}
			}
		})
	}
}

// runReportPermalinks drives the report through the real content.OptionService
// over the fake repository, so the option names -check reads stay part of what
// is asserted.
func runReportPermalinks(t *testing.T, options map[string]string) string {
	t.Helper()

	var buf bytes.Buffer
	reportPermalinks(
		context.Background(),
		&buf,
		content.NewOptionService(&fakeCLIOptionRepo{values: options}),
	)

	got := buf.String()
	if got == "" {
		t.Fatal("report is empty; -check must say something about permalinks")
	}
	return got
}

// assertNamesBase requires one line of the report to name the option and, on
// that same line, its resolved value.
//
// The option name is stripped from the line before the value is looked for,
// which is what makes the default-base cases assert anything at all: "category"
// is a substring of "category_base", so a plain Contains check would pass on a
// line that printed the option and no value. The format itself is left open --
// "category_base: sections" and `category_base resolves to "sections"` both
// satisfy this.
func assertNamesBase(t *testing.T, report, option, value string) {
	t.Helper()

	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, option) {
			continue
		}
		if strings.Contains(strings.ReplaceAll(line, option, ""), value) {
			return
		}
	}
	t.Errorf("report has no line naming %s and its resolved value %q:\n%s",
		option, value, report)
}

// fakeCLIOptionRepo is an in-memory domain.OptionRepository returning
// domain.ErrNotFound for absent names, matching the real repository's contract
// so OptionService's absent-as-empty mapping is exercised rather than bypassed.
type fakeCLIOptionRepo struct{ values map[string]string }

func (f *fakeCLIOptionRepo) Get(_ context.Context, name string) (string, error) {
	v, ok := f.values[name]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

// Duplicate-nicename fixtures for the tests below (Req 7.8). The nicenames carry
// no digits and the three numbers in each conflict are distinct and are not
// substrings of one another, because the assertions look for them as substrings
// of a report line: a fixture with Count 2 and WinnerID 2, or a nicename like
// "alice-2", would let a report that printed only one of the three numbers pass.
var (
	cliConflictAlice = domain.NicenameConflict{Nicename: "alice", Count: 2, WinnerID: 7}
	cliConflictBob   = domain.NicenameConflict{Nicename: "bob", Count: 3, WinnerID: 41}
	cliConflictCarol = domain.NicenameConflict{Nicename: "carol", Count: 5, WinnerID: 12}
)

// TestReportDuplicateNicenamesNamesEachConflict covers Requirement 7.8: the
// -check report names every duplicated user_nicename, how many rows share it,
// which ID wins under Requirement 7.6, and what that costs.
//
// All four parts are load-bearing and none substitutes for another. The nicename
// without the count reads as an error rather than a choice; the count without
// the winning ID tells an operator a collision exists but not which author is
// the one they can still reach; and all three numbers without the consequence
// are trivia, because the condition is invisible from the outside -- there is no
// error and no wrong data, just one author whose posts no longer have a URL
// (Req 7.7). -check is the surface an operator runs before serving, which is the
// whole reason this report exists rather than a per-request check (Req 7.8).
//
// The report is driven over a fake domain.NicenameAuditor rather than a
// database, because what is under test here is the reporting, and the query
// itself is already pinned across all three vendors by
// storagetest.RunNicenameAuditorContract. The fake is the narrow
// NicenameAuditor, not a UserRepository, so the interface the report depends on
// is part of what is asserted: a report reaching for the wide interface would
// not compile against this.
func TestReportDuplicateNicenamesNamesEachConflict(t *testing.T) {
	cases := []struct {
		name      string
		conflicts []domain.NicenameConflict
	}{
		{
			name:      "one duplicated nicename",
			conflicts: []domain.NicenameConflict{cliConflictAlice},
		},
		{
			// More than one conflict is what forces the three numbers onto the
			// same line as the nicename they describe: a report that printed
			// counts and winners in one block and nicenames in another would
			// leave an operator pairing them by position.
			name:      "several duplicated nicenames",
			conflicts: []domain.NicenameConflict{cliConflictAlice, cliConflictBob, cliConflictCarol},
		},
		{
			// Count is not always 2. A report that hard-coded "2 users" would
			// pass every row above and be wrong here.
			name:      "more than two rows share one nicename",
			conflicts: []domain.NicenameConflict{cliConflictCarol},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runReportDuplicateNicenames(t, tc.conflicts)
			if got == "" {
				t.Fatal("report is empty; -check must report the duplicate user_nicename values it was given")
			}
			for _, c := range tc.conflicts {
				assertReportsConflict(t, got, c)
			}
		})
	}
}

// TestReportDuplicateNicenamesIsSilentWithoutDuplicates is what makes the report
// above worth reading (Req 7.8). Duplicate nicenames are nearly always absent --
// wp_insert_user() suffixes a colliding nicename on write, so only direct SQL,
// imports and multisite merges produce them -- so a "no duplicates found" line
// would appear on essentially every run, and an operator who learns to skip it
// skips the run where it says something else. This mirrors
// TestReportPermalinksPrintsNoNotesOnACleanConfiguration one section up, and
// routing's own TestNotesEmptyWhenNothingCollides a layer below.
//
// Both shapes of "clean" are covered because both are real: *wprepo.UserRepo
// returns a nil slice for no duplicates (users.go), while a caller or a future
// backend may return an allocated empty one, and a length check treats them
// alike where a nil check does not.
func TestReportDuplicateNicenamesIsSilentWithoutDuplicates(t *testing.T) {
	cases := []struct {
		name      string
		conflicts []domain.NicenameConflict
	}{
		{name: "nil slice, as the real repository returns", conflicts: nil},
		{name: "empty slice", conflicts: []domain.NicenameConflict{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runReportDuplicateNicenames(t, tc.conflicts); got != "" {
				t.Errorf("report is not silent on a database with no duplicate "+
					"user_nicename values -- a line seen on every run is one an "+
					"operator stops reading, which hides the run that reports a "+
					"real duplicate:\n%s", got)
			}
		})
	}
}

// runReportDuplicateNicenames drives the report over a fake auditor and returns
// what it wrote.
//
// It also asserts Requirement 7.8's "SHALL run once": the report exists instead
// of a per-request check precisely because one aggregate query over
// {prefix}users is acceptable only when it happens once, so a report that called
// the auditor per conflict or per line would defeat its own justification.
func runReportDuplicateNicenames(t *testing.T, conflicts []domain.NicenameConflict) string {
	t.Helper()

	var buf bytes.Buffer
	auditor := &fakeCLINicenameAuditor{conflicts: conflicts}

	reportDuplicateNicenames(context.Background(), &buf, auditor)

	if auditor.calls != 1 {
		t.Errorf("auditor was called %d times, want exactly 1 -- the duplicate "+
			"check is one read-only aggregate query run once (Req 7.8)", auditor.calls)
	}
	return buf.String()
}

// assertReportsConflict requires one line of the report to name the nicename
// and, on that same line, both the number of rows sharing it and the winning ID.
// The three belong together: with several conflicts reported, the same numbers
// spread across separate lines say nothing about which nicename they describe.
//
// It then requires the consequence for that nicename -- the word "unreachable"
// and the author URL that loses -- somewhere in the report, rather than on that
// same line, so a report may wrap the explanation onto its own line. The URL is
// matched as "/author/{nicename}" without anchoring it to the start of a path,
// so a site whose permalink structure carries a front (Req 4.7) may print
// "/blog/author/alice" and still satisfy this.
//
// The format is otherwise left open: `  alice: 2 users share it, ID 7 wins` and
// `"alice" is shared by 2 users; ID 7 wins` both pass.
func assertReportsConflict(t *testing.T, report string, c domain.NicenameConflict) {
	t.Helper()

	count := strconv.Itoa(c.Count)
	winner := strconv.FormatInt(c.WinnerID, 10)
	found := false
	for _, line := range strings.Split(report, "\n") {
		if !strings.Contains(line, c.Nicename) {
			continue
		}
		if strings.Contains(line, count) && strings.Contains(line, winner) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("report has no line naming the duplicated nicename %q with both "+
			"the %s rows sharing it and the winning ID %s:\n%s",
			c.Nicename, count, winner, report)
	}

	// Req 7.7: one /author/{nicename} URL can surface only one of the colliding
	// authors, so the other's posts are unreachable by that route. Without this,
	// the line above is a number an operator has no reason to act on.
	lostURL := "/" + routing.AuthorBase + "/" + c.Nicename
	for _, want := range []string{lostURL, "unreachable"} {
		if !strings.Contains(report, want) {
			t.Errorf("report does not contain %q, so it states no consequence for "+
				"the duplicated nicename %q -- the other author's posts are "+
				"unreachable at %s and nothing else says so:\n%s",
				want, c.Nicename, lostURL, report)
		}
	}
}

// fakeCLINicenameAuditor is an in-memory domain.NicenameAuditor returning a
// fixed conflict list, and counting calls so the report's one-query contract can
// be asserted. It is deliberately not a full domain.UserRepository: the report
// depends on the narrow auditor interface (internal/domain/repository.go), and
// this fake is what holds it to that.
type fakeCLINicenameAuditor struct {
	conflicts []domain.NicenameConflict
	calls     int
}

func (f *fakeCLINicenameAuditor) DuplicateNicenames(context.Context) ([]domain.NicenameConflict, error) {
	f.calls++
	return f.conflicts, nil
}
