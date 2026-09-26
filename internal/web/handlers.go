package web

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/pkg/extensions"
	"html"
)

// hookRenderPostHTML is the "render.post_html" filter hook (Req 11.1):
// fired with the fully-rendered HTML buffer for a public single/page view,
// immediately before it is written to the response.
const hookRenderPostHTML = "render.post_html"

// pageParam parses a ?page= query value, defaulting to 1 for missing/invalid.
func pageParam(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return p
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) error {
	ctx := r.Context()
	title, tagline := s.options.SiteInfo(ctx)
	page := pageParam(r)
	posts, pg, err := s.posts.RecentPage(ctx, page, content.DefaultPerPage)
	if err != nil {
		return err
	}
	if page > 1 && pg.Total > 0 && page > pg.TotalPages {
		return domain.ErrNotFound
	}
	data := render.IndexData{
		SiteTitle:  title,
		Tagline:    tagline,
		Posts:      postViews(ctx, posts, s.options.BaseURLs(ctx), s.featured),
		Pagination: pg,
	}
	return s.renderHTML(w, r, "index", data)
}

// resolve is the one dispatcher every ambiguous content route is registered to.
// It classifies the request path exactly once and delegates on the result (Req
// 9.1, 9.2).
//
// Precedence therefore lives in routing.Structure.Classify -- a pure function
// with a documented table and a unit test per row -- rather than in the order
// routes were registered. That matters because chi resolves a collision at a
// parameter node silently in favour of whichever pattern was registered last
// rather than panicking (M9a probed this; see router.go's route-registration
// comment), which makes registration-order precedence unreviewable. Pointing
// every colliding pattern at this one handler is what makes chi's choice
// unobservable rather than something the code has to win.
func (s *Server) resolve(w http.ResponseWriter, r *http.Request) error {
	switch t := s.permalinks.Classify(r.URL.Path); t.Kind {
	case routing.KindPost:
		return s.single(w, r, t)
	case routing.KindCategory:
		return s.categoryArchive(w, r, t)
	case routing.KindTag:
		return s.tagArchive(w, r, t)
	case routing.KindAuthor:
		return s.authorArchive(w, r, t)
	case routing.KindDate:
		return s.dateArchive(w, r, t)
	default:
		// KindNone: the path carries neither a post nor an archive
		// interpretation. Classify is total, so this is an answer rather than a
		// failure, which is why it is a 404 and not a 500.
		return domain.ErrNotFound
	}
}

// single resolves and renders one post or page.
//
// With no permalink structure configured the flat /{slug} path is canonical and
// this is a slug lookup, exactly as it was before M9a. With a structure
// configured the handler additionally owns canonicalisation: it resolves the
// request by whichever token the structure identifies posts with, rejects date
// components that contradict the post, and permanently redirects any recognised
// path that is not the post's one canonical path (Req 2.3, 2.5, 3.1-3.4).
//
// t is the already-classified target rather than something this handler derives
// for itself, so the post path and the archive paths read one shared answer
// about what a path means and their 301 targets cannot diverge. Behavior is
// unchanged from M9a: Classify's two post rows are exactly the resolution the
// former refFrom performed -- the structure's own shape via ParamsFromPath
// followed by Match, which is still where a path like /2024/1/01/hello-1/ is
// rejected for its digit widths because chi patterns cannot express those (Req
// 2.2), and otherwise the flat single-segment fallback that makes the
// flat-to-canonical 301 reachable (Req 3.1).
func (s *Server) single(w http.ResponseWriter, r *http.Request, t routing.Target) error {
	ctx := r.Context()

	post, err := s.resolveSingle(ctx, t.Post)
	if err != nil {
		return err
	}

	// Canonical is the single construction site for a permalink, so the redirect
	// target here cannot drift from the one the REST API reports. It returns the
	// flat path when Flat, which makes this comparison a no-op rather than a
	// special case (Req 3.6).
	if canonical := s.permalinks.Canonical(post); r.URL.Path != canonical {
		return s.redirectCanonical(w, r, canonical)
	}

	return s.renderSingle(w, r, post)
}

// redirectCanonical permanently redirects to canonical, carrying the request's
// query string across unchanged.
//
// 301 rather than 302, matching WordPress's redirect_canonical: these URLs are
// permanent, and the accumulated link equity should follow. The query is
// appended **raw** rather than parsed and re-encoded, because url.Values.Encode
// sorts keys, rewrites %20 as "+" and gives a valueless parameter an "=" — a
// visitor's /?s=caf%C3%A9%20au%20lait would come back subtly different. Dropping
// it altogether is the failure that matters most: it moves a visitor reading
// page 3 of an archive back to page 1 behind a 301 that looks right.
//
// Every caller passes a path a routing.Structure constructor built from
// database-resolved rows — a post's own permalink, a term's ancestry, a
// validated date — never a request path echoed back, so a visitor cannot steer
// the target and no open redirect is reachable here.
func (s *Server) redirectCanonical(w http.ResponseWriter, r *http.Request, canonical string) error {
	target := canonical
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	http.Redirect(w, r, target, http.StatusMovedPermanently)
	return nil
}

// resolveSingle turns a classified post reference into the post it addresses,
// or domain.ErrNotFound.
//
// The ref's components came from the request path rather than from chi's
// parameter names, which is what makes this independent of which route won.
// Both forms of a single-segment structure such as /%postname%/ reach the
// dispatcher, but chi collapses that pattern against the flat /{slug} route, so
// the same post would otherwise arrive as "postname" on one form and "slug" on
// the other.
//
// The empty-ref arm is defensive rather than reachable: Parse rejects a
// structure carrying no identifying token, and Classify's flat row only claims a
// non-empty segment, so a KindPost target always carries a slug or an id.
func (s *Server) resolveSingle(ctx context.Context, ref routing.Ref) (domain.Post, error) {
	var post domain.Post
	var err error
	switch {
	case ref.Slug != "":
		post, err = s.posts.BySlug(ctx, ref.Slug)
	case ref.ID != 0:
		post, err = s.posts.PublishedByID(ctx, ref.ID)
	default:
		return domain.Post{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Post{}, err
	}

	// Req 2.5: the date in the URL must be the post's own. Only the components
	// the structure actually carries are compared, so a month-and-name structure
	// does not assert anything about the day. Dates are compared in the post's
	// stored local time, the same basis Canonical builds from.
	d := post.Date
	if (ref.Year != 0 && ref.Year != d.Year()) ||
		(ref.Month != 0 && ref.Month != int(d.Month())) ||
		(ref.Day != 0 && ref.Day != d.Day()) {
		return domain.Post{}, domain.ErrNotFound
	}

	return post, nil
}

func (s *Server) renderSingle(w http.ResponseWriter, r *http.Request, post domain.Post) error {
	ctx := r.Context()
	kind := "single"
	if post.Type == "page" {
		kind = "page"
	}
	title, tagline := s.options.SiteInfo(ctx)
	comments := []render.CommentView{}
	commentCount := 0
	var pending *render.CommentView
	commentToken := ""
	menu := render.NavMenuView{}
	if s.comments != nil {
		items, total, err := s.comments.List(ctx, domain.CommentFilter{PostID: post.ID, Statuses: []string{"1"}})
		if err != nil {
			return err
		}
		for _, c := range items {
			comments = append(comments, commentView(c))
		}
		commentCount = total
		if r.URL.Query().Get("comment") == "pending" {
			p := render.CommentView{Author: r.URL.Query().Get("author"), Content: r.URL.Query().Get("content"), Date: time.Now(), PendingEcho: true}
			p.Content = html.EscapeString(p.Content)
			pending = &p
		}
		tok, err := randToken()
		if err == nil {
			s.setCommentCSRFCookie(w, tok)
			commentToken = tok
		}
	}
	if s.menus != nil {
		m, err := s.menus.ByLocation(ctx, "primary")
		if err != nil {
			return err
		}
		menu = navMenuView(m)
	}
	data := render.SingleData{SiteTitle: title, Tagline: tagline, Post: postView(ctx, post, s.options.BaseURLs(ctx), s.featured), Comments: comments, CommentCount: commentCount, PendingComment: pending, CommentToken: commentToken, Menu: menu}
	return s.renderHTML(w, r, kind, data)
}

// The four archive handlers below follow one shape, which is what makes
// design.md's status-code table uniform across the kinds: resolve the entity
// (404 when it is absent) -> compute the canonical path from the Structure ->
// 301 to it, query string intact, when the request arrived at a different one ->
// 404 a page past the last -> render. The last three steps are renderArchive's,
// so the kinds can only differ in how they resolve their entity and where their
// canonical path comes from.
//
// Resolution comes first for a reason: /tag/nope must 404, not 301 to a
// canonical path for a tag that does not exist. That ordering costs a redirecting
// request one read it discards, which is the right trade -- the alternative
// advertises URLs that lead nowhere.

// categoryArchive serves a nested category archive (Req 2.3, 3.1).
//
// The segments come from the classifier rather than from a chi parameter,
// because a nested category path has a segment count no chi pattern can express
// (Req 9.5) -- the route is a wildcard rooted at the base.
//
// content.CategoryMovedError is the Req 2.11 recovery arriving here: the segment
// walk failed, but the path's final segment named a category somewhere in the
// taxonomy, so the published URL redirects to that category's canonical path
// instead of 404ing. It covers the flat /category/{slug} grimoire served before
// this milestone -- the zero-ancestor instance of a failed walk -- along with a
// wrong-ancestor and a skipped-level path. errors.As rather than errors.Is
// because the ancestry travels with the error; it is what the redirect target is
// built from, and CategoryMovedError deliberately does not wrap
// domain.ErrNotFound so that forgetting this arm cannot quietly 404 every legacy
// category URL on the site.
func (s *Server) categoryArchive(w http.ResponseWriter, r *http.Request, t routing.Target) error {
	archive, err := s.terms.CategoryArchive(r.Context(), t.Segments, pageParam(r), content.DefaultPerPage)
	var moved *content.CategoryMovedError
	if errors.As(err, &moved) {
		return s.redirectCanonical(w, r, s.permalinks.CategoryPath(moved.Ancestry))
	}
	if err != nil {
		return err
	}
	// The canonical path is built from the resolved term's own ancestry, not
	// from the requested segments, so it is a fixed point: a request already at
	// it renders rather than redirecting to itself (Req 2.4).
	return s.renderArchive(w, r, "category", s.permalinks.CategoryPath(archive.Ancestry), archive)
}

// tagArchive serves a tag archive (Req 5.1). post_tag is flat, so there is one
// slug and no ancestry to walk (Req 5.2).
//
// The canonical path is built from the resolved term's slug rather than from the
// requested one, so the Location is derived from the database row the way every
// other archive's is.
func (s *Server) tagArchive(w http.ResponseWriter, r *http.Request, t routing.Target) error {
	archive, err := s.terms.TagArchive(r.Context(), t.Slug, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	return s.renderArchive(w, r, "tag", s.permalinks.TagPath(archive.Term.Slug), archive)
}

// authorArchive serves an author archive (Req 7.1).
//
// The canonical path is built from the classified nicename rather than from the
// resolved user, because content.Archive carries no user: its only trace of the
// author is Heading, the display_name, so the archive cannot render a
// user_login the site had not already exposed (Req 7.4).
func (s *Server) authorArchive(w http.ResponseWriter, r *http.Request, t routing.Target) error {
	archive, err := s.posts.AuthorArchive(r.Context(), t.Slug, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	return s.renderArchive(w, r, "author", s.permalinks.AuthorPath(t.Slug), archive)
}

// dateArchive serves a date archive at whichever granularity the path carried
// (Req 6.1).
//
// This is the one kind with no entity to resolve, so nothing here can 404 for
// absence: a real date matching no post is an empty archive (Req 6.5). An
// impossible date is rejected by Classify before a handler is reached, and again
// by DateArchive, which is what makes "404 rather than querying" (Req 6.3) true
// of the service and not only of the router.
func (s *Server) dateArchive(w http.ResponseWriter, r *http.Request, t routing.Target) error {
	archive, err := s.posts.DateArchive(r.Context(), t.Date, pageParam(r), content.DefaultPerPage)
	if err != nil {
		return err
	}
	return s.renderArchive(w, r, "date", s.permalinks.DatePath(t.Date), archive)
}

// renderArchive is the shared tail of all four archive handlers: canonicalise,
// guard the page range, render.
//
// canonical is the archive's one 200-returning URL, built by a
// routing.Structure constructor -- the same construction site the theme's
// pagination links and the REST "link" field read, so the three cannot diverge.
// It is also BaseURL, which is why the templates build their pagination links
// from it instead of from Term.Slug: that form is wrong the moment a category is
// nested or a base is overridden, and an empty BaseURL renders a broken relative
// link on every paginated archive.
//
// An empty canonical is a 404 rather than a redirect to "": the constructors
// return "" for an archive the structure does not serve at all, such as a date
// archive under plain permalinks (Req 6.8). Classify does not produce those
// targets, so this is a second line of defence rather than a reachable path.
//
// The page guard is the same condition the home route applies, in the same
// order: Total > 0 so an empty archive never 404s on page 2, and after the
// canonical redirect so a non-canonical URL is corrected before its page number
// is judged (Req 8.3).
func (s *Server) renderArchive(w http.ResponseWriter, r *http.Request, kind, canonical string, a content.Archive) error {
	if canonical == "" {
		return domain.ErrNotFound
	}
	if r.URL.Path != canonical {
		return s.redirectCanonical(w, r, canonical)
	}
	if page := pageParam(r); page > 1 && a.Page.Total > 0 && page > a.Page.TotalPages {
		return domain.ErrNotFound
	}
	ctx := r.Context()
	title, tagline := s.options.SiteInfo(ctx)
	data := render.ArchiveData{
		SiteTitle:  title,
		Tagline:    tagline,
		Kind:       kind,
		Heading:    a.Heading,
		BaseURL:    canonical,
		Term:       termView(a.Term),
		Posts:      postViews(ctx, a.Posts, s.options.BaseURLs(ctx), s.featured),
		Pagination: a.Page,
	}
	return s.renderHTML(w, r, kind, data)
}

// renderHTML renders kind with data into a buffer first, so a template
// execution error surfaces before any bytes reach the client. This lets the
// error middleware map failures to a clean 404/500 instead of appending an
// error to a partially written 200 response.
//
// For the public single/page views specifically (Req 11.1), the fully
// rendered HTML buffer is passed through the "render.post_html" filter
// immediately before it is written to the response, letting a registered
// extension transform the final markup without touching the render engine
// or this handler. r may be nil (as in tests exercising only the
// render-error path); the filter is applied only when r is non-nil, since
// extensions.ApplyFilters needs a context to run under.
func (s *Server) renderHTML(w http.ResponseWriter, r *http.Request, kind string, data any) error {
	var buf bytes.Buffer
	if err := s.render.Render(&buf, kind, data); err != nil {
		return err
	}
	out := buf.Bytes()
	if r != nil && (kind == "single" || kind == "page") {
		filtered, err := extensions.ApplyFilters(r.Context(), hookRenderPostHTML, out)
		if err != nil {
			return err
		}
		out = filtered
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := w.Write(out)
	return err
}
