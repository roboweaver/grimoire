package content

import (
	"context"
	"html"
	"strconv"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/php"
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
}

// NewRESTMapper constructs a RESTMapper. prefix is the usermeta key prefix
// (e.g. "wp_") used to read a user's serialized capabilities for the roles
// field, matching UserService's own convention.
func NewRESTMapper(terms domain.PostTermsRepository, meta domain.PostMetaRepository, users domain.UserMetaRepository, prefix string) *RESTMapper {
	return &RESTMapper{terms: terms, meta: meta, users: users, prefix: prefix}
}

// postLink is the REST "link" field for a post/page: grimoire's own flat
// "/{slug}" route (see internal/web/router.go), which both post types
// share. It is a relative path; the web layer resolves it to an absolute
// URL from the request's scheme/Host at response time (Req 6.6).
func postLink(slug string) string { return "/" + slug }

// commentLink is the REST "link" field for a comment. grimoire has no
// pretty-permalink single-post route parameterized by ID, so this mirrors
// real WordPress's own "plain permalink" fallback shape
// ("/?p={id}#comment-{id}") rather than inventing a bespoke one. Like
// postLink, it is a relative path made absolute by the web layer.
func commentLink(postID, commentID int64) string {
	return "/?p=" + strconv.FormatInt(postID, 10) + "#comment-" + strconv.FormatInt(commentID, 10)
}

// userLink is the REST "link" field for a user. grimoire has no author
// archive route either, so this mirrors WordPress's own plain-permalink
// fallback ("/?author={id}") for the same reason as commentLink.
func userLink(userID int64) string { return "/?author=" + strconv.FormatInt(userID, 10) }

func restCommon(p domain.Post, featuredMedia int64) restItemCommon {
	return restItemCommon{
		ID:            p.ID,
		Date:          restDate(p.Date),
		DateGMT:       restDate(p.DateGMT),
		Modified:      restDate(p.Modified),
		ModifiedGMT:   restDate(p.ModifiedGMT),
		Slug:          p.Slug,
		Status:        p.Status,
		Type:          p.Type,
		Link:          postLink(p.Slug),
		GUID:          RESTRendered{Rendered: p.GUID},
		Title:         RESTRendered{Rendered: p.Title},
		Content:       RESTContentRendered{Rendered: p.Content, Protected: p.Password != ""},
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
		restItemCommon: restCommon(p, featured),
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
	return RESTPage{restItemCommon: restCommon(p, featured)}, nil
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
		Content:    RESTRendered{Rendered: html.EscapeString(c.Content)},
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
		Link: userLink(u.ID),
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
