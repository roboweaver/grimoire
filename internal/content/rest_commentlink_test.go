package content

import (
	"context"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/routing"
)

// commentLinkFallback is the plain-permalink-shaped fallback Req 10.4 keeps, for
// the fixture pair used throughout this file: comment 9 on post 42
// (samplePost()). Written as a literal so nothing in the assertion can be
// computed by the code under test.
const commentLinkFallback = "/?p=42#comment-9"

// newTestMapperWithBases is newTestMapperWithPermalinks with the two archive
// base options as well, which is the input Phase 8 threads through link
// construction (Req 4.1, 4.2, 10.2). The existing helper hard-codes both bases
// empty, so it cannot express the configuration where the base is the thing that
// changed.
func newTestMapperWithBases(t *testing.T, structure, categoryBase, tagBase string) *RESTMapper {
	t.Helper()
	st, err := routing.Parse(structure, categoryBase, tagBase)
	if err != nil {
		t.Fatalf("routing.Parse(%q, %q, %q): %v", structure, categoryBase, tagBase, err)
	}
	terms := &fakeRESTPostTerms{byPost: map[int64]map[string][]int64{}}
	meta := &fakeRESTPostMeta{featured: map[int64]int64{}, attach: map[int64]string{}}
	users := &fakeRESTUserMeta{values: map[int64]map[string]string{}}
	return NewRESTMapper(terms, meta, users, "wp_").WithPermalinks(st)
}

// TestRESTCommentLinkStaysOnPlainPermalinkFallback covers Req 10.4: commentLink
// keeps "/?p={id}#comment-{id}" while the links either side of it move —
// restTermLink to the archive paths (Req 10.2) and userLink to /author/{nicename}
// (Req 10.3). No comment route is added in this milestone, so a comment link
// routed through routing.Structure would advertise a path no route serves.
//
// This is a guard, not a red test: the value it carries is failing if tasks
// 8.3/8.4 sweep commentLink along with its neighbours.
//
// It adds two inputs the existing
// TestRESTCommentLinkUnchangedByPermalinkStructure does not vary:
//
//   - the archive base options, which are what Phase 8 threads into link
//     construction — a sweep that reached comments would most plausibly arrive
//     through that new plumbing rather than through the structure alone;
//   - a structure with a front, and the %post_id% structure, which is the one
//     configuration where an ID-keyed path is actually served (/archives/42), so
//     the one where a swept implementation would look correct by coincidence.
//
// Each case also asserts the link is *not* the post permalink with a comment
// fragment appended, because that — Canonical(post) + "#comment-{id}" — is the
// shape a rewrite would reach for, and it is the one a bare string comparison
// against the fallback would catch only by luck if the fallback constant were
// ever edited to match.
func TestRESTCommentLinkStaysOnPlainPermalinkFallback(t *testing.T) {
	cases := []struct {
		name         string
		structure    string
		categoryBase string
		tagBase      string
	}{
		{
			name:      "flat structure",
			structure: "",
		},
		{
			name:      "post name",
			structure: restStructPostName,
		},
		{
			name:      "day and name",
			structure: restStructDayAndName,
		},
		{
			// The only structure under which an ID-keyed path resolves a post at
			// all, so the only one where routing a comment link through the
			// structure would not obviously 404.
			name:      "numeric",
			structure: restStructNumeric,
		},
		{
			// Req 4.7's front applies to archive paths; it must not reach a link
			// that is not an archive path.
			name:      "structure with a front",
			structure: restStructFrontDayAndName,
		},
		{
			// Req 4.1/4.2: the bases the term and author links now honor. A
			// comment is neither, so neither base may appear in its link.
			name:         "overridden category and tag bases",
			structure:    restStructDayAndName,
			categoryBase: "sections",
			tagBase:      "labels",
		},
		{
			name:         "overridden bases on a flat structure",
			structure:    "",
			categoryBase: "sections",
			tagBase:      "labels",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mapper := newTestMapperWithBases(t, tc.structure, tc.categoryBase, tc.tagBase)

			c := Comment(domain.Comment{ID: 9, PostID: 42, Status: commentStatusOK})
			if c.Link != commentLinkFallback {
				t.Errorf("comment Link = %q, want %q (Req 10.4: no comment route exists, so this link must stay the plain-permalink fallback)",
					c.Link, commentLinkFallback)
			}

			// The post whose permalink a swept implementation would have
			// borrowed. samplePost() is id 42, so it is the same post the
			// comment hangs off.
			post, err := mapper.Post(context.Background(), samplePost())
			if err != nil {
				t.Fatalf("Post: %v", err)
			}
			if fragmented := post.Link + "#comment-9"; c.Link == fragmented {
				t.Errorf("comment Link = %q, which is the post permalink plus a fragment: commentLink has been routed through the permalink structure",
					c.Link)
			}
		})
	}
}

// TestRESTCommentLinkTakesNoPermalinkStructure pins the mechanism rather than
// the string: Comment is a plain function with no access to a
// routing.Structure, which is what makes the invariant above structural instead
// of coincidental. If Phase 8 gives comments a structure-aware link it will do
// so by moving Comment onto *RESTMapper, and this file stops compiling — which
// is the intended failure mode, since the compile error names the decision Req
// 10.4 made.
//
// The assertion available to a test is the observable half of that: two mappers
// configured as differently as the option surface allows produce the same
// comment link, because neither mapper is consulted.
func TestRESTCommentLinkTakesNoPermalinkStructure(t *testing.T) {
	flat := newTestMapperWithBases(t, "", "", "")
	configured := newTestMapperWithBases(t, restStructFrontDayAndName, "sections", "labels")

	// Both mappers see the same post, and disagree about its permalink: that
	// disagreement is what a comment link routed through either of them would
	// inherit.
	flatPost, err := flat.Post(context.Background(), samplePost())
	if err != nil {
		t.Fatalf("Post (flat): %v", err)
	}
	configuredPost, err := configured.Post(context.Background(), samplePost())
	if err != nil {
		t.Fatalf("Post (configured): %v", err)
	}
	if flatPost.Link == configuredPost.Link {
		t.Fatalf("post Link is %q under both mappers: the fixture no longer distinguishes the two configurations",
			flatPost.Link)
	}

	c := Comment(domain.Comment{ID: 9, PostID: 42, Status: commentStatusOK})
	if c.Link != commentLinkFallback {
		t.Errorf("comment Link = %q, want %q", c.Link, commentLinkFallback)
	}
}
