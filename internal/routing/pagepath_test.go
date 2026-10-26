package routing_test

import (
	"slices"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// M15 task 1.1 -- TDD RED step for hierarchical page URLs.
//
// This file is written BEFORE the production code (task 1.2) exists, so it is
// expected NOT to compile until 1.2 lands. It references three symbols the
// design specifies but that do not exist yet; the compile failure naming them is
// the correct RED signal:
//
//   - routing.KindPage       -- the new Kind constant appended after KindDate
//                               (design.md "routing.KindPage + the classifier
//                               row"; Req 6.3).
//   - routing.Structure.PagePath(ancestry []string) string
//                             -- the new PURE path builder modeled on
//                               CategoryPath (design.md "routing.Structure.
//                               PagePath"; Req 1, 4).
//   - domain.Post.PageAncestry []string
//                             -- the TRANSIENT, non-persisted field the resolved
//                               page carries so Canonical's page guard can read
//                               its walked ancestry via pageAncestryFor(p) while
//                               Canonical stays a pure function of its single
//                               argument (design.md "Page ancestry attached to a
//                               resolved domain.Post"; Req 4, 8.4).
//
// CHOSEN SEAM (1.2 MUST implement exactly this so these tests go GREEN):
//
//   // PageAncestry is a transient, non-persisted routing/transport field. It is
//   // never written to storage and is not part of the domain post reader/writer
//   // method set (Req 8.4). When set, it is the root-first slug ancestry of a
//   // resolved page, ending in the page's own slug (self-inclusive leaf), that
//   // Canonical's page guard reads via pageAncestryFor(p).
//   PageAncestry []string
//
//   // pageAncestryFor(p domain.Post) []string returns p.PageAncestry when it is
//   // non-empty; otherwise, when p.Type == "page", it degrades to the one-element
//   // []string{p.Slug} (a top-level page), so Canonical of a bare page is /{slug}
//   // and never "" (Req 1.2, 4.5). For a non-page it is never consulted, because
//   // Canonical's page guard fires only for p.Type == "page".
//
//   // Canonical gains a single guard at its top, otherwise byte-for-byte
//   // unchanged for a post (Req 4.2):
//   //   if p.Type == "page" { return s.PagePath(pageAncestryFor(p)) }
//
// These tests DO depend on that seam landing in 1.2; the Canonical page-branch
// cases below set domain.Post.PageAncestry directly to exercise it.

// pagePost builds a page the way the resolution path would hand it to Canonical:
// Type "page" with its walked, root-first, self-inclusive ancestry attached. The
// Date is deliberately non-zero and non-UTC-safe so a case that wrongly fell into
// the dated post branch would produce an obviously dated path in the failure.
func pagePost(slug string, ancestry []string) domain.Post {
	return domain.Post{
		ID:           42,
		Slug:         slug,
		Type:         "page",
		PageAncestry: ancestry,
		Date:         time.Date(2024, 5, 17, 13, 45, 0, 0, time.UTC),
	}
}

// Structure.PagePath -- the pure builder. A page is EXEMPT from
// permalink_structure, so PagePath adds NO front and NO structure tokens; it is
// exactly the ancestry slugs joined root-first, with the structure's trailing
// slash. An empty ancestry yields "" (the "no servable canonical" guard,
// mirroring CategoryPath's empty-ancestry case). Req 1.1, 1.2, 4.1, 4.5.
func TestPagePath(t *testing.T) {
	cases := []struct {
		name      string
		structure string
		ancestry  []string
		want      string
	}{
		{
			// One-element ancestry under a trailing-slash structure -> /{slug}/.
			// Req 1.2, 4.5.
			name:      "one element ancestry, trailing slash structure",
			structure: presetPostName,
			ancestry:  []string{"about"},
			want:      "/about/",
		},
		{
			// One-element ancestry under a flat structure -> /{slug}, byte for
			// byte the flat top-level page URL grimoire serves today. Req 1.2,
			// 4.5.
			name:      "one element ancestry, flat structure",
			structure: "",
			ancestry:  []string{"about"},
			want:      "/about",
		},
		{
			// A multi-element ancestry yields the nested path with NO front and
			// NO structure tokens -- even under a dated/day-and-name structure,
			// because a page is exempt from permalink_structure. Req 1.1, 4.1.
			name:      "multi element ancestry carries no front or tokens, trailing slash",
			structure: presetDayAndName,
			ancestry:  []string{"about", "team", "leads"},
			want:      "/about/team/leads/",
		},
		{
			// The same nested ancestry under a flat structure drops the trailing
			// slash but is otherwise identical: still no tokens, still root-first.
			name:      "multi element ancestry, flat structure, no trailing slash",
			structure: "",
			ancestry:  []string{"about", "team"},
			want:      "/about/team",
		},
		{
			// A custom structure that happens to carry a literal front/prefix
			// must NOT leak into a page path. Req 1.4, 4.1.
			name:      "multi element ancestry ignores a literal front in the structure",
			structure: presetNumeric, // "/archives/%post_id%"
			ancestry:  []string{"about", "team"},
			// presetNumeric has no trailing slash, so no trailing slash here.
			want: "/about/team",
		},
		{
			// An empty ancestry yields "" -- the handler treats "" as "no
			// servable canonical" and 404s, mirroring CategoryPath's empty case.
			// A page the walk could not place must not redirect to the site root.
			name:      "empty ancestry yields empty string",
			structure: presetPostName,
			ancestry:  nil,
			want:      "",
		},
		{
			name:      "empty (non-nil) ancestry yields empty string",
			structure: presetDayAndName,
			ancestry:  []string{},
			want:      "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.structure, err)
			}
			if got := s.PagePath(tc.ancestry); got != tc.want {
				t.Errorf("PagePath(%q) under %q = %q, want %q",
					tc.ancestry, tc.structure, got, tc.want)
			}
		})
	}
}

// Structure.Canonical page branch -- a page (built with the transient ancestry
// field set, read by the guard via pageAncestryFor(p)) returns its nested
// ancestry path and NEVER a dated/post-shape path, under flat, dated,
// day-and-name, and custom structures. Req 1.4, 4.1.
func TestCanonicalPageBranchNeverDated(t *testing.T) {
	// A page nested two deep. Under EVERY structure its canonical is the nested
	// ancestry, so the post-shape branch (which would produce a dated or
	// id-based path from the Date/ID) must never run for a page.
	page := pagePost("leads", []string{"about", "team", "leads"})

	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{"flat", "", "/about/team/leads"},
		{"post name", presetPostName, "/about/team/leads/"},
		{"month and name (dated)", presetMonthAndName, "/about/team/leads/"},
		{"day and name (dated)", presetDayAndName, "/about/team/leads/"},
		{"numeric custom", presetNumeric, "/about/team/leads"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.structure, err)
			}
			got := s.Canonical(page)
			if got != tc.want {
				t.Errorf("Canonical(page) under %q = %q, want nested %q",
					tc.structure, got, tc.want)
			}
			// Guard the real risk directly: a page canonical must carry none of
			// the date components the post-shape branch would emit.
			for _, dated := range []string{"2024", "05", "17"} {
				if slices.Contains(pathSegmentsForTest(got), dated) {
					t.Errorf("Canonical(page) under %q = %q leaked a dated "+
						"segment %q -- a page is exempt from permalink_structure",
						tc.structure, got, dated)
				}
			}
		})
	}
}

// A top-level page with no attached ancestry degrades to /{slug}: pageAncestryFor
// falls back to []string{p.Slug} when PageAncestry is empty and p.Type == "page",
// so Canonical of a bare page is /{slug} (trailing slash per the structure) and
// never "". Req 1.2, 4.5.
func TestCanonicalPageBranchTopLevelDegradesToSlug(t *testing.T) {
	// No ancestry attached -- the top-level case.
	page := domain.Post{
		ID:   7,
		Slug: "about",
		Type: "page",
		Date: time.Date(2024, 5, 17, 0, 0, 0, 0, time.UTC),
	}
	cases := []struct {
		name      string
		structure string
		want      string
	}{
		{"flat", "", "/about"},
		{"post name", presetPostName, "/about/"},
		{"day and name (dated)", presetDayAndName, "/about/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.structure, err)
			}
			if got := s.Canonical(page); got != tc.want {
				t.Errorf("Canonical(top-level page) under %q = %q, want %q",
					tc.structure, got, tc.want)
			}
		})
	}
}

// A post is byte-for-byte unchanged by the page guard: the Canonical page branch
// fires only for p.Type == "page", so a post keeps the exact post-shape permalink
// it has today. Asserted against the same vectors TestCanonical uses. Req 4.2.
func TestCanonicalPostUnchangedByPageGuard(t *testing.T) {
	post := domain.Post{
		ID:   400774,
		Slug: "hello-world",
		Type: "post",
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
		{"flat falls back to slug", "", "/hello-world"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.structure, err)
			}
			if got := s.Canonical(post); got != tc.want {
				t.Errorf("Canonical(post) under %q = %q, want %q (unchanged by "+
					"the page guard)", tc.structure, got, tc.want)
			}
		})
	}
}

// Classify row placement -- the KindPage row sits after the archive rows and
// after row 7 (post shape), before row 8 (the flat /{slug} fallback).
//
//   - a two-plus-segment non-archive non-post path -> KindPage with
//     Target.Segments set to the full segment list (Req 6.3 outcome via the walk);
//   - an archive base path still classifies as its archive Kind (Req 6.4);
//   - a post-shape path still -> KindPost (Req 6.5);
//   - a one-segment path still -> KindPost (Req 6.1).
//
// The existing per-row category/tag/author/date/post tests in classify_test.go
// stay unchanged and are the Req 6 regression guard; this test only adds the new
// KindPage expectations.
func TestClassifyPageRowPlacement(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		path         string
		want         routing.Kind
		wantSegments []string        // read for KindPage and KindCategory
		wantPost     routing.Ref     // read for KindPost
		wantDate     routing.DateRef // read for KindDate
	}{
		{
			// Two segments, no archive base, not the structure's post shape
			// (presetPostName is single-segment) -> KindPage carrying both
			// segments for the handler's page walk. Req 6.3.
			name:         "two segment non-archive non-post path is KindPage",
			structure:    presetPostName,
			path:         "/about/team/",
			want:         routing.KindPage,
			wantSegments: []string{"about", "team"},
		},
		{
			// Three segments, same reasoning -- the full segment list is carried.
			name:         "three segment non-archive non-post path is KindPage",
			structure:    presetPostName,
			path:         "/about/team/leads/",
			want:         routing.KindPage,
			wantSegments: []string{"about", "team", "leads"},
		},
		{
			// An archive base wins over the page row: the category rows run first,
			// so a colliding path is KindCategory, not KindPage. Req 6.4.
			name:         "archive base path still classifies as its archive Kind",
			structure:    presetPostName,
			path:         "/category/news/",
			want:         routing.KindCategory,
			wantSegments: []string{"news"},
		},
		{
			// A multi-segment path matching the structure's own post shape stays
			// KindPost -- row 7 runs before the page row. Req 6.5.
			name:      "post-shape path still classifies as KindPost",
			structure: presetMonthAndName,
			path:      "/2024/05/hello-world/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Year: 2024, Month: 5, Slug: "hello-world"},
		},
		{
			// A single-segment path stays KindPost (row 8 flat fallback); the
			// page row claims only two-or-more segments, so no top-level page URL
			// changes. Req 6.1.
			name:      "one segment path still classifies as KindPost",
			structure: presetPostName,
			path:      "/about/",
			want:      routing.KindPost,
			wantPost:  routing.Ref{Slug: "about"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := routing.Parse(tc.structure, "", "")
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.structure, err)
			}
			got := s.Classify(tc.path)
			if got.Kind != tc.want {
				t.Fatalf("Classify(%q) under %q Kind = %v, want %v",
					tc.path, tc.structure, got.Kind, tc.want)
			}
			switch tc.want {
			case routing.KindPage, routing.KindCategory:
				if !slices.Equal(got.Segments, tc.wantSegments) {
					t.Errorf("Classify(%q) Segments = %q, want %q",
						tc.path, got.Segments, tc.wantSegments)
				}
			case routing.KindPost:
				if got.Post != tc.wantPost {
					t.Errorf("Classify(%q) Post = %+v, want %+v",
						tc.path, got.Post, tc.wantPost)
				}
			case routing.KindDate:
				if got.Date != tc.wantDate {
					t.Errorf("Classify(%q) Date = %+v, want %+v",
						tc.path, got.Date, tc.wantDate)
				}
			}
		})
	}
}

// pathSegmentsForTest splits a path into non-empty segments, local to this test
// file so the dated-leak assertion above does not reach into unexported package
// internals.
func pathSegmentsForTest(path string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			if i > start {
				out = append(out, path[start:i])
			}
			start = i + 1
		}
	}
	return out
}
