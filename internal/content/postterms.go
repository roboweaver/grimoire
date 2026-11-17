package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
)

// ErrTaxonomyNotAttached is returned when a registered post type is assigned
// terms of a taxonomy that is not attached to it (including unknown names).
// Nothing is written. Handlers map it to 400.
var ErrTaxonomyNotAttached = errors.New("content: taxonomy is not attached to this post type")

// ErrTermInvalid is returned when a term id is not a term of the requested
// taxonomy (it does not exist or belongs to another taxonomy). Nothing is
// written. Handlers map it to 400.
var ErrTermInvalid = errors.New("content: term id is not a term of the taxonomy")

// CapabilitySuffixFor returns the capability suffix that governs typ and the
// registered entry when typ is a registered (non-built-in) type. Built-in and
// unregistered types use the legacy page/post families. reg may be nil.
func CapabilitySuffixFor(reg *registry.Registry, typ string) (string, registry.PostType, bool) {
	if reg != nil && typ != "" && typ != "post" && typ != "page" {
		if pt, ok := reg.PostType(typ); ok {
			return auth.CapabilitySuffix(pt.CapabilityType), pt, true
		}
	}
	if typ == "page" {
		return auth.CapabilitySuffix("page"), registry.PostType{}, false
	}
	return auth.CapabilitySuffix("post"), registry.PostType{}, false
}

// PostTermsWriteService assigns taxonomy terms to a post. Assigning terms is
// authorized as part of editing the post itself (auth.CanEditPost against the
// target post's current stored record, loaded via PostWriter.ByID) — design.md
// deliberately does not gate this behind the separate manage_categories
// capability that term Create/Update/Delete require, since choosing which of
// the site's existing terms apply to a post is an editing action on the post,
// not a taxonomy-management action.
type PostTermsWriteService struct {
	posts postByID
	w     domain.PostTermsWriter
	reg   *registry.Registry
	terms domain.TermReader
}

// PostTermsOption configures optional PostTermsWriteService collaborators.
type PostTermsOption func(*PostTermsWriteService)

// WithPostTermsRegistry enables the registered-type rules (capability family,
// taxonomy attachment).
func WithPostTermsRegistry(r *registry.Registry) PostTermsOption {
	return func(s *PostTermsWriteService) { s.reg = r }
}

// WithPostTermsReader supplies the term reader used to validate that ids are
// terms of the requested taxonomy on registered types.
func WithPostTermsReader(r domain.TermReader) PostTermsOption {
	return func(s *PostTermsWriteService) { s.terms = r }
}

// NewPostTermsWriteService constructs a PostTermsWriteService from a post
// reader (used only to load the authoritative record for authorization) and a
// PostTermsWriter.
func NewPostTermsWriteService(posts postByID, w domain.PostTermsWriter, opts ...PostTermsOption) *PostTermsWriteService {
	s := &PostTermsWriteService{posts: posts, w: w}
	for _, o := range opts {
		o(s)
	}
	return s
}

// checkTerms applies the registered-type rules: the taxonomy must be attached
// to pt and every id must be a term of it. Empty ids always pass once attached.
func (s *PostTermsWriteService) checkTerms(ctx context.Context, pt registry.PostType, taxonomy string, ids []int64) error {
	attached := false
	for _, t := range pt.Taxonomies {
		if t == taxonomy {
			attached = true
			break
		}
	}
	if !attached {
		return ErrTaxonomyNotAttached
	}
	if len(ids) == 0 {
		return nil
	}
	if s.terms == nil {
		return errors.New("content: term ids cannot be validated without a term reader")
	}
	return s.checkMembership(ctx, taxonomy, ids)
}

// checkMembership verifies every id is a term of taxonomy.
func (s *PostTermsWriteService) checkMembership(ctx context.Context, taxonomy string, ids []int64) error {
	found, err := s.terms.TermsByIDs(ctx, ids)
	if err != nil {
		return err
	}
	ok := make(map[int64]bool, len(found))
	for _, t := range found {
		if t.Taxonomy == taxonomy {
			ok[t.ID] = true
		}
	}
	for _, id := range ids {
		if !ok[id] {
			return ErrTermInvalid
		}
	}
	return nil
}

// CheckAssignable reports whether actor could assign termIDs of taxonomy to a
// new draft of typ that they author, without writing. Capability is checked
// first, then attachment, then term membership.
func (s *PostTermsWriteService) CheckAssignable(ctx context.Context, actor auth.Principal, typ, taxonomy string, termIDs []int64) error {
	suf, pt, registered := CapabilitySuffixFor(s.reg, typ)
	if !auth.CanEditPostSuffix(actor, suf, "draft", actor.UserID) {
		return ErrForbidden
	}
	if !registered {
		return nil
	}
	return s.checkTerms(ctx, pt, taxonomy, termIDs)
}

// SetPostTerms authorizes against postID's current stored record and, if
// permitted, replaces its taxonomy term relationships with exactly termIDs. A
// missing post returns the generic ErrForbidden (existence is not leaked),
// matching PostWriteService.Update/Delete's convention.
func (s *PostTermsWriteService) SetPostTerms(ctx context.Context, actor auth.Principal, postID int64, taxonomy string, termIDs []int64) error {
	cur, err := s.posts.ByID(ctx, postID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return ErrForbidden
		}
		return err
	}
	if cur.Type == "" {
		cur.Type = "post"
	}
	if !WritableType(s.reg, cur.Type) {
		return ErrForbidden
	}
	suf, pt, registered := CapabilitySuffixFor(s.reg, cur.Type)
	if !auth.CanEditPostSuffix(actor, suf, cur.Status, cur.Author) {
		return ErrForbidden
	}
	if !ManagedTaxonomy(s.reg, taxonomy) {
		return ErrTaxonomyUnknown
	}
	if registered {
		if err := s.checkTerms(ctx, pt, taxonomy, termIDs); err != nil {
			return err
		}
	} else if len(termIDs) > 0 && s.terms != nil {
		if err := s.checkMembership(ctx, taxonomy, termIDs); err != nil {
			return err
		}
	}
	return s.w.SetPostTerms(ctx, postID, taxonomy, termIDs)
}
