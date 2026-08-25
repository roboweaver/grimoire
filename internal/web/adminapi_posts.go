package web

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
)

// adminDateLayout is the ISO-8601 layout every admin API date/modified field
// uses on both the request and response side (Req 4.2).
const adminDateLayout = "2006-01-02T15:04:05Z07:00"

// taxonomyPostTag mirrors content's unexported taxonomyPostTag constant,
// which internal/web cannot import directly. content.TaxonomyCategory is
// exported and reused as-is.
const taxonomyPostTag = "post_tag"

// adminKnownTaxonomies lists every taxonomy the admin API resolves terms for
// (Req 2.1/4.1's terms map, Req 2.2's termIds map keys). It is a small,
// hardcoded list rather than a registry lookup because grimoire, like the
// spec's Req 2.6 notes, does not enforce taxonomy-to-post-type registration
// anywhere else either.
var adminKnownTaxonomies = []string{content.TaxonomyCategory, taxonomyPostTag}

// postAdminWriter is the narrow write surface adminPostCreate/Update/Delete
// depend on; *content.PostWriteService satisfies it.
type postAdminWriter interface {
	Create(ctx context.Context, actor auth.Principal, p domain.Post) (int64, error)
	Update(ctx context.Context, actor auth.Principal, p domain.Post, expectedModified time.Time) error
	Delete(ctx context.Context, actor auth.Principal, p domain.Post) error
}

// postTermsAdminWriter is the narrow write surface adminPostCreate/Update
// depend on to apply a request's termIds map; *content.PostTermsWriteService
// satisfies it.
type postTermsAdminWriter interface {
	SetPostTerms(ctx context.Context, actor auth.Principal, postID int64, taxonomy string, termIDs []int64) error
}

// postTermsAdminReader resolves a post's currently-assigned term IDs for a
// taxonomy, used to populate Req 4.1's terms map; domain.PostTermsRepository
// (already implemented by wprepo for the M5 REST mapper) satisfies it.
type postTermsAdminReader interface {
	TermsForPost(ctx context.Context, postID int64, taxonomy string) ([]int64, error)
}

// postWriteRequest is the JSON body shape POST/PUT /admin/api/posts accept
// (Req 1.1/1.2).
type postWriteRequest struct {
	Title         string             `json:"title"`
	Content       string             `json:"content"`
	Excerpt       string             `json:"excerpt"`
	Slug          string             `json:"slug"`
	Status        string             `json:"status"`
	Type          string             `json:"type"`
	Date          string             `json:"date"`
	CommentStatus string             `json:"commentStatus"`
	Modified      string             `json:"modified"`
	TermIDs       map[string][]int64 `json:"termIds"`
}

// validPostStatuses is the exact vocabulary Req 5.1 accepts.
var validPostStatuses = map[string]bool{
	"draft":   true,
	"pending": true,
	"publish": true,
	"private": true,
	"future":  true,
}

// parsePostWrite validates body per Req 1.7/1.8/5.1/5.2 and returns the
// domain.Post to hand to the write service. current, when non-nil, is the
// post's existing stored record (only supplied for updates) and backs the
// Req 5.2 unchanged-date exception. It never touches the write service.
func (s *Server) parsePostWrite(body postWriteRequest, current *domain.Post) (domain.Post, error) {
	typ := body.Type
	if typ == "" {
		typ = "post"
	}
	if typ != "post" && typ != "page" {
		return domain.Post{}, badRequestError{msg: "type must be \"post\" or \"page\""}
	}
	status := body.Status
	if status == "" {
		status = "draft"
	}
	if !validPostStatuses[status] {
		return domain.Post{}, badRequestError{msg: "invalid status"}
	}
	if body.Title == "" && status != "draft" {
		return domain.Post{}, badRequestError{msg: "title is required unless status is draft"}
	}
	var date time.Time
	if body.Date != "" {
		var err error
		date, err = time.Parse(adminDateLayout, body.Date)
		if err != nil {
			return domain.Post{}, badRequestError{msg: "invalid date"}
		}
	}
	if status == "future" && !date.IsZero() && !date.After(time.Now()) {
		// Req 5.2 exception: an update that resubmits the post's own
		// currently-stored date unchanged is allowed through even though
		// that date is no longer in the future, so fixing an unrelated
		// field on a stale future post never forces resolving its schedule.
		unchanged := current != nil && current.Date.Equal(date)
		if !unchanged {
			return domain.Post{}, badRequestError{msg: "future status requires a date in the future"}
		}
	}
	return domain.Post{
		Title:         body.Title,
		Content:       body.Content,
		Excerpt:       body.Excerpt,
		Slug:          body.Slug,
		Status:        status,
		Type:          typ,
		Date:          date,
		CommentStatus: body.CommentStatus,
	}, nil
}

// postDetail builds the shared Req 4.1 detail response for p, resolving its
// assigned terms across every known taxonomy.
func (s *Server) postDetail(ctx context.Context, p domain.Post) (postDetailResponse, error) {
	terms, err := s.resolvePostTerms(ctx, p.ID)
	if err != nil {
		return postDetailResponse{}, err
	}
	return postDetailResponse{
		ID:            p.ID,
		Title:         p.Title,
		Slug:          p.Slug,
		Type:          p.Type,
		Status:        p.Status,
		Author:        p.Author,
		Date:          p.Date.UTC().Format(adminDateLayout),
		Modified:      p.Modified.UTC().Format(adminDateLayout),
		Excerpt:       p.Excerpt,
		Content:       p.Content,
		CommentStatus: p.CommentStatus,
		Terms:         terms,
	}, nil
}

// resolvePostTerms builds the terms map Req 4.1 requires: for every known
// taxonomy, the term IDs currently assigned to postID resolved into
// {id,name,slug} summaries. A post with no terms in a taxonomy still gets an
// empty (non-nil) slice entry so clients don't need a presence check.
func (s *Server) resolvePostTerms(ctx context.Context, postID int64) (map[string][]termSummary, error) {
	out := make(map[string][]termSummary, len(adminKnownTaxonomies))
	if s.postTermsRead == nil || s.termWrite == nil {
		return out, nil
	}
	for _, taxonomy := range adminKnownTaxonomies {
		ids, err := s.postTermsRead.TermsForPost(ctx, postID, taxonomy)
		if err != nil {
			return nil, err
		}
		summaries := make([]termSummary, 0, len(ids))
		if len(ids) > 0 {
			terms, err := s.termWrite.TermsByIDs(ctx, ids)
			if err != nil {
				return nil, err
			}
			for _, t := range terms {
				summaries = append(summaries, termSummary{ID: t.ID, Name: t.Name, Slug: t.Slug})
			}
			sort.Slice(summaries, func(i, j int) bool { return summaries[i].Name < summaries[j].Name })
		}
		out[taxonomy] = summaries
	}
	return out, nil
}

// applyTermIDs applies body's termIds map (if any) via SetPostTerms, one
// taxonomy at a time, after the post write itself already succeeded (Req
// 2.2). A per-taxonomy failure is collected into the returned partial map
// rather than propagated, since the post write must not be rolled back.
func (s *Server) applyTermIDs(ctx context.Context, actor auth.Principal, postID int64, termIDs map[string][]int64) map[string]string {
	if len(termIDs) == 0 || s.postTermsWrite == nil {
		return nil
	}
	var partial map[string]string
	for taxonomy, ids := range termIDs {
		if err := s.postTermsWrite.SetPostTerms(ctx, actor, postID, taxonomy, ids); err != nil {
			if partial == nil {
				partial = map[string]string{}
			}
			partial[taxonomy] = err.Error()
		}
	}
	return partial
}

// adminPostCreate handles POST /admin/api/posts (Req 1.1).
func (s *Server) adminPostCreate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	var body postWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	p, err := s.parsePostWrite(body, nil)
	if err != nil {
		return err
	}
	id, err := s.postWrite.Create(r.Context(), principal, p)
	if err != nil {
		return err
	}
	partial := s.applyTermIDs(r.Context(), principal, id, body.TermIDs)
	stored, err := s.admin.Detail(r.Context(), id)
	if err != nil {
		return err
	}
	resp, err := s.postDetail(r.Context(), stored)
	if err != nil {
		return err
	}
	resp.Partial = partial
	return writeJSON(w, http.StatusCreated, resp)
}

// adminPostUpdate handles PUT /admin/api/posts/{id} (Req 1.2).
func (s *Server) adminPostUpdate(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	var body postWriteRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return badRequestError{msg: "invalid request body"}
	}
	if body.Modified == "" {
		return badRequestError{msg: "modified is required"}
	}
	expectedModified, err := time.Parse(adminDateLayout, body.Modified)
	if err != nil {
		return badRequestError{msg: "invalid modified"}
	}
	// Req 5.2's unchanged-date exception needs the post's current stored
	// date; a 403/404 on this internal read is safe to propagate as-is,
	// since it reveals no more than the write below already would for the
	// same input.
	var current *domain.Post
	if cur, err := s.admin.Detail(r.Context(), id); err == nil {
		current = &cur
	}
	p, err := s.parsePostWrite(body, current)
	if err != nil {
		return err
	}
	p.ID = id
	if err := s.postWrite.Update(r.Context(), principal, p, expectedModified); err != nil {
		return err
	}
	partial := s.applyTermIDs(r.Context(), principal, id, body.TermIDs)
	stored, err := s.admin.Detail(r.Context(), id)
	if err != nil {
		return err
	}
	resp, err := s.postDetail(r.Context(), stored)
	if err != nil {
		return err
	}
	resp.Partial = partial
	return writeJSON(w, http.StatusOK, resp)
}

// adminPostDelete handles DELETE /admin/api/posts/{id} (Req 1.3).
func (s *Server) adminPostDelete(w http.ResponseWriter, r *http.Request) error {
	principal, _ := PrincipalFrom(r.Context())
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return badRequestError{msg: "invalid post id"}
	}
	if err := s.postWrite.Delete(r.Context(), principal, domain.Post{ID: id}); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
