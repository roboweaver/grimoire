package content

import (
	"bytes"
	"context"
	stdhtml "html"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
	"github.com/roboweaver/grimoire/internal/routing"
)

// RESTContext selects which WordPress REST "context" a view model is
// rendered for. Real WordPress varies certain fields (most notably on
// users, Req 5.1-5.2) by context; grimoire mirrors that vocabulary even
// though only the user mapping in this file currently branches on it.
type RESTContext string

// The two REST contexts this milestone implements. WordPress also has a
// third context, "embed" (a cut-down shape used inside _embedded), which
// M5 does not need to distinguish from "view" for any of its resources.
const (
	RESTContextView RESTContext = "view"
	RESTContextEdit RESTContext = "edit"
)

// restDateFormat matches WordPress's own REST date rendering: a naive
// (no-timezone-suffix) ISO 8601 datetime. WordPress uses this exact format
// for both the site-local date/modified fields and the *_gmt variants (the
// latter simply carrying the UTC-normalized value in the same shape).
const restDateFormat = "2006-01-02T15:04:05"

// taxonomyPostTag is the WordPress taxonomy name for tags. TaxonomyCategory
// (term.go) is its counterpart for categories.
const taxonomyPostTag = "post_tag"

func restDate(t time.Time) string { return t.Format(restDateFormat) }

// RESTRendered is WordPress's {"rendered": "..."} sub-object shape, used for
// the title/excerpt/guid fields.
type RESTRendered struct {
	Rendered string `json:"rendered"`
}

// RESTContentRendered is the WordPress content sub-object shape: rendered
// HTML plus a "protected" flag, true when the post has a non-empty
// post_password (Req 2.2).
type RESTContentRendered struct {
	Rendered  string `json:"rendered"`
	Protected bool   `json:"protected"`
}

// restItemCommon holds the fields shared by the posts and pages view models.
// It is embedded (not referenced) by RESTPost/RESTPage so encoding/json
// flattens it into the parent object rather than nesting it under a key.
type restItemCommon struct {
	ID            int64               `json:"id"`
	Date          string              `json:"date"`
	DateGMT       string              `json:"date_gmt"`
	Modified      string              `json:"modified"`
	ModifiedGMT   string              `json:"modified_gmt"`
	Slug          string              `json:"slug"`
	Status        string              `json:"status"`
	Type          string              `json:"type"`
	Link          string              `json:"link"`
	GUID          RESTRendered        `json:"guid"`
	Title         RESTRendered        `json:"title"`
	Content       RESTContentRendered `json:"content"`
	Excerpt       RESTRendered        `json:"excerpt"`
	Author        int64               `json:"author"`
	FeaturedMedia int64               `json:"featured_media"`
	CommentStatus string              `json:"comment_status"`
	PingStatus    string              `json:"ping_status"`
}

// RESTPost is the WordPress REST shape for a wp/v2/posts item (Req 2.2).
type RESTPost struct {
	restItemCommon
	Categories []int64 `json:"categories"`
	Tags       []int64 `json:"tags"`
}

// RESTPage is the WordPress REST shape for a wp/v2/pages item. Real
// WordPress pages do not register the category/post_tag taxonomies, so
// (unlike RESTPost) categories/tags are absent from the JSON entirely
// rather than present-but-empty (Req 2.2).
type RESTPage struct {
	restItemCommon
}

// RESTComment is the WordPress REST shape for a wp/v2/comments item
// (Req 3.2). Content.Rendered is HTML-escaped, matching the M4
// public-comment trust boundary (untrusted user input), unlike posts'
// trusted Content.Rendered.
type RESTComment struct {
	ID         int64        `json:"id"`
	Post       int64        `json:"post"`
	Parent     int64        `json:"parent"`
	AuthorName string       `json:"author_name"`
	AuthorURL  string       `json:"author_url"`
	Date       string       `json:"date"`
	DateGMT    string       `json:"date_gmt"`
	Content    RESTRendered `json:"content"`
	Link       string       `json:"link"`
	Status     string       `json:"status"`
}

// RESTMediaDetails is the WordPress media_details sub-object (Req 4.1). Only
// width/height are populated (from the attachment's stored
// _wp_attachment_metadata postmeta); a non-image attachment, or one with no
// stored metadata, gets the empty object {} WordPress itself returns, via
// the omitempty zero-value default.
type RESTMediaDetails struct {
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
}

// RESTMedia is the WordPress REST shape for a wp/v2/media item (Req 4.1).
type RESTMedia struct {
	ID           int64            `json:"id"`
	Date         string           `json:"date"`
	Slug         string           `json:"slug"`
	Type         string           `json:"type"`
	Link         string           `json:"link"`
	Title        RESTRendered     `json:"title"`
	Author       int64            `json:"author"`
	MimeType     string           `json:"mime_type"`
	SourceURL    string           `json:"source_url"`
	Post         int64            `json:"post"`
	MediaDetails RESTMediaDetails `json:"media_details"`
	Caption      RESTRendered     `json:"caption"`
	AltText      string           `json:"alt_text"`
	Description  RESTRendered     `json:"description"`
}

// RESTUser is the WordPress REST "view" context shape for a wp/v2/users
// item: the fields visible to an anonymous or uncapable requester
// (Req 5.1).
type RESTUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
	Link string `json:"link"`
}

// RESTUserEdit is the WordPress REST "edit" context shape: the view fields
// plus email/url/roles, gated on list_users or "viewing your own record"
// (Req 5.2). It never carries a password hash, application-password
// record, session token, or CSRF secret (Req 5.3) — those are simply not
// domain.User fields this mapping ever reads.
type RESTUserEdit struct {
	RESTUser
	Email string   `json:"email"`
	URL   string   `json:"url"`
	Roles []string `json:"roles"`
}

// RESTMapper builds WordPress REST view models from grimoire's existing
// domain types, composing the additive PostTermsRepository/
// PostMetaRepository/UserMetaRepository ports (Req 2.6, 4.2, 5.4). It
// performs no authorization and no visibility filtering — exactly like
// AdminService, the web layer is responsible for deciding which posts/
// users/context a caller is allowed to see (Req 2.4, 5.2) before handing
// the resolved value to this mapper.
type RESTMapper struct {
	terms  domain.PostTermsRepository
	meta   domain.PostMetaRepository
	users  domain.UserMetaRepository
	prefix string

	// permalinks is the resolved permalink_structure, held once here rather
	// than threaded through every mapping call. NewRESTMapper defaults it to
	// a flat structure, so a mapper that never saw WithPermalinks produces
	// the pre-M9a "/{slug}" links rather than carrying an ambiguous zero
	// value (M9a Req 6.1, 6.2).
	permalinks routing.Structure

	// uploadsBaseURL anchors srcset candidates when an <img>'s src carries no
	// directory of its own (Finding 3): with urlDir(src) == "", a dir(src)
	// anchor would emit bare basenames, so rewriteImageNode falls back to this
	// base instead. NewRESTMapper defaults it to WordPress's conventional
	// "/wp-content/uploads" so a mapper that never saw WithUploadsBaseURL still
	// produces a usable, site-root-relative anchor rather than an empty one.
	uploadsBaseURL string

	// pages is the page-hierarchy reader postLink walks to populate a page row's
	// transient PageAncestry before Canonical builds the nested link (M15,
	// Req 4.4). It is the single home of the page-ancestry walk (Req 4.3), shared
	// with the handler entry points. nil is the deliberate default: a mapper that
	// never saw WithPages leaves PageAncestry unset, so Canonical's page guard
	// degrades a page to /{slug} exactly as before M15 (Req 4.5).
	pages *PageService
}

// NewRESTMapper constructs a RESTMapper. prefix is the usermeta key prefix
// (e.g. "wp_") used to read a user's serialized capabilities for the roles
// field, matching UserService's own convention.
func NewRESTMapper(terms domain.PostTermsRepository, meta domain.PostMetaRepository, users domain.UserMetaRepository, prefix string) *RESTMapper {
	// An empty structure is WordPress's "plain" setting and never fails to
	// parse, so the error is not reachable here. Mirrors web.NewServer.
	flat, _ := routing.Parse("", "", "")
	return &RESTMapper{terms: terms, meta: meta, users: users, prefix: prefix, permalinks: flat, uploadsBaseURL: "/wp-content/uploads"}
}

// WithPermalinks configures the permalink structure used to build the REST
// "link" field. It returns the same RESTMapper for chaining, matching
// web.Server.WithPermalinks, so the startup path can hand the same parsed
// Structure to both without either growing a positional parameter.
//
// The Structure is supplied already parsed so the caller owns the fallback
// decision: routing.Parse returns a usable flat Structure alongside its error.
func (m *RESTMapper) WithPermalinks(st routing.Structure) *RESTMapper {
	m.permalinks = st
	return m
}

// WithUploadsBaseURL configures the uploads base that anchors srcset candidates
// when an <img>'s src has no directory of its own (Finding 3). It returns the
// same RESTMapper for chaining, matching WithPermalinks, so the startup path can
// hand it the resolved uploads URL alongside the permalink structure without
// either growing a positional parameter.
func (m *RESTMapper) WithUploadsBaseURL(base string) *RESTMapper {
	m.uploadsBaseURL = base
	return m
}

// WithPages configures the page-hierarchy reader postLink uses to populate a
// page row's transient PageAncestry before Canonical builds the link (M15,
// Req 4.4). It returns the same RESTMapper for chaining, mirroring
// WithPermalinks/WithUploadsBaseURL, so the startup path can hand it the shared
// *PageService alongside the permalink structure without either growing a
// positional parameter. A mapper that never saw WithPages keeps pre-M15
// behavior: a page's link degrades to /{slug} via Canonical's page guard.
func (m *RESTMapper) WithPages(p *PageService) *RESTMapper {
	m.pages = p
	return m
}

// postLink is the REST "link" field for a post/page, both post types sharing
// one route. It delegates to routing.Structure.Canonical — the single
// construction site for a permalink — so the advertised link and the path the
// web layer serves at 200 cannot diverge (Req 6.1, 8.5). A flat structure yields
// "/{slug}" exactly as before M9a (Req 6.2). It is a relative path; the web
// layer resolves it to an absolute URL from the request's scheme/Host at
// response time (Req 6.6).
//
// For a page with a wired page service (M15), it first walks the page graph to
// populate p's transient PageAncestry, so Canonical's page guard builds the
// nested canonical (/handbook/onboarding/) rather than the /{slug} degrade
// (Req 4.4). p is a value parameter, so this mutates only the local copy — the
// transient field exists solely to feed the pure Canonical and is never
// persisted (D4). A post's link, and a page's link when no page service is
// wired, are unchanged: in both those cases Canonical is called exactly as
// before (Req 4.2, 4.5). No second page-URL builder is introduced — Canonical
// remains the single construction site (Req 4.3, 8.5).
//
// If the ancestry walk errors, postLink falls back gracefully: it leaves
// PageAncestry unset so Canonical degrades to /{slug} rather than failing the
// whole REST mapping. A link field should not 500 the response over a
// best-effort nested-canonical enrichment.
func (m *RESTMapper) postLink(ctx context.Context, p domain.Post) string {
	if p.Type == "page" && m.pages != nil {
		if ancestry, err := m.pages.AncestryFor(ctx, p.ID); err == nil {
			p.PageAncestry = ancestry
		}
	}
	return m.permalinks.Canonical(p)
}

// commentLink is the REST "link" field for a comment. grimoire has no
// pretty-permalink single-post route parameterized by ID, so this mirrors
// real WordPress's own "plain permalink" fallback shape
// ("/?p={id}#comment-{id}") rather than inventing a bespoke one. Like
// postLink, it is a relative path made absolute by the web layer.
func commentLink(postID, commentID int64) string {
	return "/?p=" + strconv.FormatInt(postID, 10) + "#comment-" + strconv.FormatInt(commentID, 10)
}

// userLink is the REST "link" field for a user. It delegates to
// routing.Structure.AuthorPath — the single construction site for an author
// archive URL — so the advertised link is the path web.authorArchive serves at
// 200 rather than the "/?author={id}" fallback M9a kept explicitly because no
// author route existed (Req 7.5, 10.3). AuthorPath applies the permalink front
// unconditionally (Req 4.7) and the structure's trailing-slash rule, so a flat
// structure yields "/author/{nicename}" with no trailing slash.
//
// It is keyed on user_nicename, the field /author/{nicename} resolves by
// (Req 7.1), which is why the ID is only the degenerate fallback: a row with an
// empty user_nicename addresses no author archive, and AuthorPath returns "" for
// it. Advertising "" would resolve to the site root, so such a row keeps the
// plain-permalink shape — the one URL that still distinguishes it — rather than
// pointing at the home page. Like postLink, the result is a relative path made
// absolute by the web layer.
func (m *RESTMapper) userLink(u domain.User) string {
	if path := m.permalinks.AuthorPath(u.Nicename); path != "" {
		return path
	}
	return "/?author=" + strconv.FormatInt(u.ID, 10)
}

// restCommon is a method rather than a plain function because the link field
// now depends on the mapper's permalink structure. It also threads ctx so the
// content field can be routed through The Transform (renderContent).
func (m *RESTMapper) restCommon(ctx context.Context, p domain.Post, featuredMedia int64) restItemCommon {
	return restItemCommon{
		ID:            p.ID,
		Date:          restDate(p.Date),
		DateGMT:       restDate(p.DateGMT),
		Modified:      restDate(p.Modified),
		ModifiedGMT:   restDate(p.ModifiedGMT),
		Slug:          p.Slug,
		Status:        p.Status,
		Type:          p.Type,
		Link:          m.postLink(ctx, p),
		GUID:          RESTRendered{Rendered: p.GUID},
		Title:         RESTRendered{Rendered: p.Title},
		Content:       RESTContentRendered{Rendered: m.renderContent(ctx, p.Content), Protected: p.Password != ""},
		Excerpt:       RESTRendered{Rendered: Excerpt(p)},
		Author:        p.Author,
		FeaturedMedia: featuredMedia,
		CommentStatus: p.CommentStatus,
		PingStatus:    p.PingStatus,
	}
}

// Post maps a domain.Post (post_type "post") into its REST view model,
// populating categories/tags via PostTermsRepository and featured_media via
// PostMetaRepository (Req 2.2, 2.6).
func (m *RESTMapper) Post(ctx context.Context, p domain.Post) (RESTPost, error) {
	featured, err := m.meta.FeaturedMediaID(ctx, p.ID)
	if err != nil {
		return RESTPost{}, err
	}
	categories, err := m.terms.TermsForPost(ctx, p.ID, TaxonomyCategory)
	if err != nil {
		return RESTPost{}, err
	}
	tags, err := m.terms.TermsForPost(ctx, p.ID, taxonomyPostTag)
	if err != nil {
		return RESTPost{}, err
	}
	if categories == nil {
		categories = []int64{}
	}
	if tags == nil {
		tags = []int64{}
	}
	return RESTPost{
		restItemCommon: m.restCommon(ctx, p, featured),
		Categories:     categories,
		Tags:           tags,
	}, nil
}

// Page maps a domain.Post (post_type "page") into its REST view model.
// Pages carry featured_media like posts do, but never categories/tags
// (Req 2.2).
func (m *RESTMapper) Page(ctx context.Context, p domain.Post) (RESTPage, error) {
	featured, err := m.meta.FeaturedMediaID(ctx, p.ID)
	if err != nil {
		return RESTPage{}, err
	}
	return RESTPage{restItemCommon: m.restCommon(ctx, p, featured)}, nil
}

// restCommentStatus maps the raw comment_approved enum ("0"/"1"/"spam"/
// "trash") to WordPress's REST status vocabulary (Req 3.2). An unrecognized
// raw value (should not occur; comments.go only ever writes the four known
// values) is passed through unchanged rather than panicking.
func restCommentStatus(raw string) string {
	switch raw {
	case commentStatusHold:
		return "hold"
	case commentStatusOK:
		return "approved"
	case commentStatusSpam:
		return "spam"
	case commentStatusTrash:
		return "trash"
	default:
		return raw
	}
}

// Comment maps a domain.Comment into its REST view model. It requires no
// port lookups, so it is a plain function rather than a RESTMapper method.
func Comment(c domain.Comment) RESTComment {
	return RESTComment{
		ID:         c.ID,
		Post:       c.PostID,
		Parent:     c.Parent,
		AuthorName: c.Author,
		AuthorURL:  c.AuthorURL,
		Date:       restDate(c.Date),
		DateGMT:    restDate(c.DateGMT),
		Content:    RESTRendered{Rendered: stdhtml.EscapeString(c.Content)},
		Link:       commentLink(c.PostID, c.ID),
		Status:     restCommentStatus(c.Status),
	}
}

// mediaDetailsFromSerialized decodes an attachment's _wp_attachment_metadata
// postmeta value (PHP-serialized) into the REST media_details shape,
// tolerating an empty, missing, or malformed value by returning the empty
// {} WordPress itself sends for non-image attachments (Req 4.1).
func mediaDetailsFromSerialized(raw string) RESTMediaDetails {
	if raw == "" {
		return RESTMediaDetails{}
	}
	v, err := php.Unserialize(raw)
	if err != nil {
		return RESTMediaDetails{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return RESTMediaDetails{}
	}
	return RESTMediaDetails{Width: phpInt(m["width"]), Height: phpInt(m["height"])}
}

// sizeVariant is one entry of an attachment's registered image sizes (the
// "sizes" sub-map of _wp_attachment_metadata): a resized file plus its pixel
// dimensions. It is the raw material srcsetFromSizes (task 3.x) turns into a
// WordPress srcset; sizesFromMetadata only decodes and filters, leaving
// ordering and URL construction to that later consumer.
type sizeVariant struct {
	File   string
	Width  int
	Height int
}

// sizesFromMetadata decodes an attachment's _wp_attachment_metadata postmeta
// into its registered size variants plus the full-size width, tolerating a
// missing/empty/malformed blob the same way mediaDetailsFromSerialized does:
// a nil meta port, an AttachmentMetadata error, or an empty/unparseable value
// is "no variants" ((nil, 0)), never a surfaced error (Req 3.3; Finding 2). A
// blob with a top-level width but no "sizes" key keeps the full-size width so
// a caller can still omit srcset while using the dimensions ((nil, fullWidth),
// Req 2.5). Entries missing a file name or with width <= 0 are skipped rather
// than emitted as unusable srcset candidates. It is an independent sibling of
// mediaDetailsFromSerialized and does not alter that function's width/height
// behavior (Req 3.4).
func (m *RESTMapper) sizesFromMetadata(ctx context.Context, id int64) ([]sizeVariant, int) {
	if m.meta == nil {
		return nil, 0
	}
	raw, err := m.meta.AttachmentMetadata(ctx, id)
	if err != nil || raw == "" {
		return nil, 0
	}
	v, err := php.Unserialize(raw)
	if err != nil {
		return nil, 0
	}
	top, ok := v.(map[string]any)
	if !ok {
		return nil, 0
	}
	fullW := phpInt(top["width"])
	sizesMap, ok := top["sizes"].(map[string]any)
	if !ok {
		return nil, fullW
	}
	variants := make([]sizeVariant, 0, len(sizesMap))
	for _, sv := range sizesMap {
		entry, ok := sv.(map[string]any)
		if !ok {
			continue
		}
		file, _ := entry["file"].(string)
		w := phpInt(entry["width"])
		if file == "" || w <= 0 {
			continue
		}
		variants = append(variants, sizeVariant{File: file, Width: w, Height: phpInt(entry["height"])})
	}
	return variants, fullW
}

// srcsetFromSizes builds the WordPress srcset/sizes attribute pair from an
// attachment's registered size variants. It sorts a copy of variants ascending
// by width (never mutating the caller's slice), renders each as
// "{dir}{file} {width}w" where dir is urlDir(base), and joins them with ", "
// (Req 2.2). The sizes string is "(max-width: {w}px) 100vw, {w}px" for a
// positive fullWidth and empty otherwise, so a zero width never emits a
// nonsensical "0px" form (Req 2.3, 2.5).
func srcsetFromSizes(base string, variants []sizeVariant, fullWidth int) (srcset, sizes string) {
	cp := append([]sizeVariant(nil), variants...)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Width < cp[j].Width })
	dir := urlDir(base)
	candidates := make([]string, 0, len(cp))
	for _, v := range cp {
		candidates = append(candidates, dir+v.File+" "+strconv.Itoa(v.Width)+"w")
	}
	srcset = strings.Join(candidates, ", ")
	if fullWidth > 0 {
		sizes = "(max-width: " + strconv.Itoa(fullWidth) + "px) 100vw, " + strconv.Itoa(fullWidth) + "px"
	}
	return srcset, sizes
}

// resolveAttachmentID extracts a WordPress attachment id from an <img>'s class
// list by scanning its space-separated tokens for a "wp-image-{digits}" entry
// (Req 3.1). It never reads src, so a URL that merely contains "wp-image-" does
// not match; a zero, absent, or non-numeric id yields (0, false).
func resolveAttachmentID(n *html.Node) (int64, bool) {
	for _, tok := range strings.Fields(attrValue(n, "class")) {
		rest, ok := strings.CutPrefix(tok, "wp-image-")
		if !ok {
			continue
		}
		id, err := strconv.ParseInt(rest, 10, 64)
		if err == nil && id > 0 {
			return id, true
		}
	}
	return 0, false
}

// urlDir returns everything up to and including the final "/" in s, or "" when
// s contains no slash. It is the directory prefix srcsetFromSizes prepends to
// each variant file name.
func urlDir(s string) string {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return ""
	}
	return s[:i+1]
}

// attrValue returns the value of n's first attribute named key, or "" when no
// such attribute is present.
func attrValue(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// setIfAbsent appends a key=val attribute to n only when no attribute named key
// already exists, leaving an existing value untouched and never duplicating it
// (Req 2.6 idempotence).
func setIfAbsent(n *html.Node, key, val string) {
	for _, a := range n.Attr {
		if a.Key == key {
			return
		}
	}
	n.Attr = append(n.Attr, html.Attribute{Key: key, Val: val})
}

// containsIMG is the cheap, case-folded probe renderContent uses to decide
// whether the far more expensive parse/rewrite path is worth entering at all.
// A case-insensitive strings.Contains for "<img" catches "<IMG"/"<Img" alike;
// a false positive (the literal text "<img" outside a real tag) merely costs a
// parse that finds no atom.Img, never a wrong rewrite.
func containsIMG(s string) bool {
	return strings.Contains(strings.ToLower(s), "<img")
}

// renderContent is "The Transform": it strips Gutenberg block comments and then
// enhances the post's <img> tags (loading/decoding/srcset/sizes). It strips
// first via the shared StripBlockComments, then short-circuits — returning the
// stripped string untouched — when there is no attachment metadata port to read
// (m.meta == nil, the D6/Finding-2 nil-guard) or the stripped content holds no
// <img> at all. Only when both an image and a meta port are present does it
// delegate to rewriteImages. The short-circuit is also the Req 4.5 byte-identical
// guarantee: comment-free, image-free content never reaches html.ParseFragment/
// html.Render, so no serializer round-trip can perturb it.
func (m *RESTMapper) renderContent(ctx context.Context, rendered string) string {
	stripped := StripBlockComments(rendered)
	if m.meta == nil || !containsIMG(stripped) {
		return stripped
	}
	return m.rewriteImages(ctx, stripped)
}

// rewriteImages parses s as an HTML fragment, enhances every <img>, and
// re-serializes it. It parses with html.ParseFragment against a bare
// &html.Node{body} context node — not a full-document parse — so the output is a
// fragment with no synthesized <html>/<head>/<body> wrapper (D3). x/net/html is
// forgiving and rarely errors; the ParseFragment error guard returns s unchanged
// as the belt-and-braces Req 5.3 parse-failure passthrough. Each top-level node
// is walked for <img> descendants, then re-rendered with a per-node html.Render
// into a shared buffer so the fragment's node order is preserved; a Render error
// likewise falls back to the untouched s.
func (m *RESTMapper) rewriteImages(ctx context.Context, s string) string {
	ctxNode := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := html.ParseFragment(strings.NewReader(s), ctxNode)
	if err != nil {
		return s
	}
	for _, n := range nodes {
		m.walkAndRewrite(ctx, n)
	}
	var buf bytes.Buffer
	for _, n := range nodes {
		if err := html.Render(&buf, n); err != nil {
			return s
		}
	}
	return buf.String()
}

// walkAndRewrite descends n depth-first, calling rewriteImageNode on every
// element whose atom is atom.Img and recursing into every child so <img> tags
// nested inside other elements (figures, links, paragraphs) are reached too.
func (m *RESTMapper) walkAndRewrite(ctx context.Context, n *html.Node) {
	if n.Type == html.ElementNode && n.DataAtom == atom.Img {
		m.rewriteImageNode(ctx, n)
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		m.walkAndRewrite(ctx, c)
	}
}

// rewriteImageNode enhances a single <img>. loading="lazy" and decoding="async"
// are always set-if-absent, even for an <img> whose attachment id can't be
// resolved, since they need no metadata (Req 3.2). It then resolves the
// wp-image-{id} class to an attachment id — returning early if none is present —
// and reads that attachment's registered size variants; with no variants it
// stops after loading/decoding (Req 2.5, 3.3). Otherwise it builds the
// srcset/sizes pair anchored on the directory of the img's own src, falling back
// to m.uploadsBaseURL when src carries no directory (urlDir(src) == "", the
// Finding-3 fallback): srcsetFromSizes derives its dir from urlDir(base), so a
// base ending in "/" anchors candidates on exactly that uploads directory. Both
// attributes are set-if-absent and only when a non-empty srcset was produced, so
// an author-supplied srcset is never overwritten (Req 2.6, 5.4 idempotence).
func (m *RESTMapper) rewriteImageNode(ctx context.Context, n *html.Node) {
	setIfAbsent(n, "loading", "lazy")
	setIfAbsent(n, "decoding", "async")
	id, ok := resolveAttachmentID(n)
	if !ok {
		return
	}
	variants, fullW := m.sizesFromMetadata(ctx, id)
	if len(variants) == 0 {
		return
	}
	base := attrValue(n, "src")
	if urlDir(base) == "" {
		base = strings.TrimRight(m.uploadsBaseURL, "/") + "/"
	}
	srcset, sizes := srcsetFromSizes(base, variants, fullW)
	if srcset != "" {
		setIfAbsent(n, "srcset", srcset)
		setIfAbsent(n, "sizes", sizes)
	}
}

// phpInt normalizes a decoded PHP scalar (int from php.Unserialize's integer
// parser, or occasionally float64/int64 from other decode paths) to a Go
// int, defaulting to 0 for anything else (including a missing/nil key).
func phpInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}

// Media maps a domain.Media into its REST view model, populating
// media_details via PostMetaRepository (Req 4.1, 4.2). link and source_url
// both reuse domain.Media.URL unchanged (Req 4.3): grimoire has no separate
// attachment permalink page, so link mirrors source_url rather than
// inventing one, and source_url is exactly the M4 admin media listing's
// existing url field (already an absolute-from-site-root path; the web
// layer makes it a fully absolute URL per Req 6.6).
func (m *RESTMapper) Media(ctx context.Context, media domain.Media) (RESTMedia, error) {
	raw, err := m.meta.AttachmentMetadata(ctx, media.ID)
	if err != nil {
		return RESTMedia{}, err
	}
	return RESTMedia{
		ID:           media.ID,
		Date:         restDate(media.Date),
		Slug:         media.Slug,
		Type:         "attachment",
		Link:         media.URL,
		Title:        RESTRendered{Rendered: media.Title},
		Author:       media.AuthorID,
		MimeType:     media.MimeType,
		SourceURL:    media.URL,
		Post:         media.ParentID,
		MediaDetails: mediaDetailsFromSerialized(raw),
		Caption:      RESTRendered{Rendered: media.Caption},
		AltText:      media.AltText,
		Description:  RESTRendered{Rendered: media.Description},
	}, nil
}

// User maps a domain.User into its REST view model. In RESTContextView it
// returns a RESTUser; in RESTContextEdit it returns a RESTUserEdit (view
// fields plus email/url/roles), reading roles from the user's serialized
// {prefix}capabilities usermeta the same way UserService writes them
// (Req 5.1, 5.2). A missing or malformed capabilities value yields an empty
// Roles slice rather than an error, matching PrincipalForUser's tolerance
// for the same data.
func (m *RESTMapper) User(ctx context.Context, u domain.User, restContext RESTContext) (any, error) {
	view := RESTUser{
		ID:   u.ID,
		Name: u.DisplayName,
		Slug: u.Nicename,
		Link: m.userLink(u),
	}
	if restContext != RESTContextEdit {
		return view, nil
	}
	roles := []string{}
	if serialized, err := m.users.Get(ctx, u.ID, m.prefix+"capabilities"); err == nil {
		if parsed, err := auth.ParseCapabilities(serialized); err == nil {
			roles = parsed
		}
	}
	return RESTUserEdit{
		RESTUser: view,
		Email:    u.Email,
		URL:      u.URL,
		Roles:    roles,
	}, nil
}
