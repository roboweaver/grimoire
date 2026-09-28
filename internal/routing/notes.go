package routing

import (
	"fmt"
	"slices"
)

// Option keys as an operator sees them in {prefix}options. Notes quote these
// rather than the Go field names, because the thing an operator has to change is
// the option, not Structure.CategoryBase.
const (
	categoryBaseOption = "category_base"
	tagBaseOption      = "tag_base"
)

// Notes carries non-fatal diagnostics about a parsed Structure: base-segment
// collisions (with each other, with "author", or with a leading literal of the
// permalink structure) and a base that normalizes to more than one segment.
// Empty when nothing is wrong.
//
// Deliberately NOT reported through Parse's error return. M9a gives that error
// exactly one meaning at every call site -- "unsupported structure, fall back to
// the flat route" -- and a site that renamed its category base has given no
// reason to stop serving permalinks. Reusing the error channel here would turn a
// cosmetic option clash into a site-wide permalink outage (Req 4.5).
//
// Each entry names the option at fault and the value it resolved to, so the line
// is actionable on its own: a note saying only "base collision detected" leaves
// an operator reading source code. Where two readings of a segment compete the
// note also names the one that won, because the two outcomes are not
// interchangeable -- a base colliding with another base costs one archive
// surface, while a base colliding with the structure's front (Req 4.4b) would
// have cost every post URL on the site had the archive not been the surface that
// degrades.
//
// The notes are computed once, by Parse, and returned as a copy so a caller
// logging them cannot alter what the next caller reads. A Structure not produced
// by Parse reports none.
func (s Structure) Notes() []string { return slices.Clone(s.notes) }

// baseOptionNotes returns the diagnostics decidable from the resolved bases
// alone: the three base-segment collisions of Req 4.4a and the multi-segment
// bases of Req 4.12. It reads no segments and no front, which is why Parse can
// record it on the flat fallback it returns alongside ErrUnsupported -- that
// fallback still serves /{CategoryBase}/{slug} and /{TagBase}/{slug}
// (ArchivePatterns returns both for a Flat structure), so a collision between
// those two bases is just as real there.
//
// Each condition is tested independently rather than in an else-if chain: a site
// that renamed both bases to "author" has three separate faults, and reporting
// only the first leaves an operator fixing a third of the problem.
//
// The values compared are the normalized ones (Req 4.11), because those are what
// the patterns, the classifier and the path constructors consume -- a comparison
// against the raw option values would miss the admin-UI shape "/sections/"
// colliding with "sections" and leave the clash silent.
func (s Structure) baseOptionNotes() []string {
	var notes []string

	if s.CategoryBase == s.TagBase {
		notes = append(notes, fmt.Sprintf(
			"%s and %s both resolve to %q: the colliding segment keeps its "+
				"category meaning, so tag archives are unreachable at /%s/{slug} "+
				"while both options name the same base",
			categoryBaseOption, tagBaseOption, s.CategoryBase, s.CategoryBase))
	}
	if s.CategoryBase == AuthorBase {
		notes = append(notes, fmt.Sprintf(
			"%s resolves to %q, the author archive base: the colliding segment "+
				"keeps its category meaning, so author archives are unreachable "+
				"at /%s/{nicename}",
			categoryBaseOption, AuthorBase, AuthorBase))
	}
	if s.TagBase == AuthorBase {
		notes = append(notes, fmt.Sprintf(
			"%s resolves to %q, the author archive base: the colliding segment "+
				"keeps its tag meaning, so author archives are unreachable at "+
				"/%s/{nicename}",
			tagBaseOption, AuthorBase, AuthorBase))
	}

	notes = append(notes, multiSegmentBaseNote(categoryBaseOption, s.CategoryBase)...)
	notes = append(notes, multiSegmentBaseNote(tagBaseOption, s.TagBase)...)

	return notes
}

// multiSegmentBaseNote reports a base that normalizes to more than one segment
// (Req 4.12). It is a note and not a rejection: the multi-segment value is used
// consistently by the patterns, the classifier and the constructors, so the
// archive works -- it just looks a segment or two longer than an operator
// probably intended, which is worth saying once at startup rather than leaving
// to be discovered from a URL.
//
// The note quotes the normalized value, since that is the base actually served.
// Quoting "//topics//news/" would describe a base that no route carries.
func multiSegmentBaseNote(option, base string) []string {
	if len(baseSegments(base)) < 2 {
		return nil
	}
	return []string{fmt.Sprintf(
		"%s resolves to %q, which is more than one path segment: it is used as "+
			"given in the archive routes, the archive links and classification",
		option, base)}
}

// frontCollisionNotes returns the Req 4.4b diagnostics: a resolved base equal to
// a leading literal segment of the permalink structure -- the front itself, or
// its first segment when the front is longer than one.
//
// It is separate from baseOptionNotes because it needs the derived front, which
// Parse only has once the structure has parsed. The flat fallback therefore
// carries none of these, correctly: it has no front for a base to collide with.
//
// The note states that the **post** meaning wins, because that is the half an
// operator cannot see. chi registers /archives/{post_id} alongside /archives/*
// without complaining (probed directly), so the configuration is silently
// half-broken: every post URL keeps working and the archive base simply never
// matches. Without this line there is nothing anywhere saying which of the two
// surfaces was sacrificed.
func (s Structure) frontCollisionNotes() []string {
	var notes []string
	for _, b := range []struct{ option, base string }{
		{categoryBaseOption, s.CategoryBase},
		{tagBaseOption, s.TagBase},
	} {
		if !s.baseCollidesWithFront(b.base) {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"%s resolves to %q, which is also a leading literal segment of the "+
				"permalink structure %q: the post meaning wins for that segment, "+
				"so every post URL keeps resolving and that archive base is "+
				"unreachable instead",
			b.option, b.base, s.Raw))
	}
	return notes
}
