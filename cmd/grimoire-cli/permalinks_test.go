package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
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

// TestReportPermalinksOmitsTheArchiveBases pins the deliberate omission.
// routing.Parse resolves category_base and tag_base, but no route honors either
// one in this milestone -- the category archive is still registered at the
// literal "/category/{slug}" and tag archives are not served at all. Reporting
// a resolved base would tell an operator their override is in effect when it is
// not, so -check stays silent about both until a route actually uses them.
func TestReportPermalinksOmitsTheArchiveBases(t *testing.T) {
	var buf bytes.Buffer
	opts := content.NewOptionService(&fakeCLIOptionRepo{values: map[string]string{
		content.OptionPermalinkStructure: cliStructDayAndName,
		content.OptionCategoryBase:       "sections",
		content.OptionTagBase:            "topics",
	}})

	reportPermalinks(context.Background(), &buf, opts)

	got := buf.String()
	for _, absent := range []string{"sections", "topics"} {
		if strings.Contains(got, absent) {
			t.Errorf("report names archive base %q, which no route honors yet:\n%s", absent, got)
		}
	}
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
