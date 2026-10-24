package content

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// ErrInvalidMenuURL is returned by NavMenuWriteService when a custom menu item's
// URL fails validation: it does not parse, or it carries a scheme outside the
// acceptable set (http/https/mailto/tel/ftp, plus relative paths). Rejecting a
// bad URL rather than storing it un-vetted is the fail-closed URL path of
// Req 4.3/4.6; the web layer maps this sentinel to a 400 (design Finding 5).
var ErrInvalidMenuURL = errors.New("content: invalid menu item URL")

// errInvalidMenuURL is the internal alias used by validateCustomURL.
var errInvalidMenuURL = ErrInvalidMenuURL

// ErrUnknownMenuItemType is returned by NavMenuWriteService when a menu item's
// declared type is outside the accepted set {custom, post_type, taxonomy}
// (resolved open question 3, Req 6.2). It fails closed before the writer and is
// mapped to a 400 at the web layer.
var ErrUnknownMenuItemType = errors.New("content: unknown menu item type")

// ErrInvalidMenuParent is returned by NavMenuWriteService when a non-zero parent
// item ID does not resolve to another nav_menu_item linked to the same menu
// (Req 5.4). It fails closed before the writer and is mapped to a 400.
var ErrInvalidMenuParent = errors.New("content: parent item not in menu")

// NavMenuWriteService performs create/update/delete of nav menus and items,
// routing every caller-supplied name and label through the shared
// sanitize.Policy and validating every custom URL before persistence (Req 4, 9).
// It is the menu analogue of MediaWriteService/TermWriteService.
type NavMenuWriteService struct {
	w      domain.NavMenuWriter
	reader domain.NavMenuRepository // read-back after write, and slug-uniqueness lookup
	// policy defaults to sanitize.New() and is NEVER nil: an unwired service
	// sanitizes rather than silently passing input through (fail closed,
	// Req 4, 9.1), matching PostWriteService / MediaWriteService.
	policy *sanitize.Policy
}

// NavMenuWriteOption configures optional NavMenuWriteService dependencies.
type NavMenuWriteOption func(*NavMenuWriteService)

// WithNavMenuContentPolicy injects the process-wide *sanitize.Policy. When
// omitted, the constructor's sanitize.New() default applies, so the service
// never leaves itself with a nil Policy (Req 3.4, 4).
func WithNavMenuContentPolicy(p *sanitize.Policy) NavMenuWriteOption {
	return func(s *NavMenuWriteService) { s.policy = p }
}

// NewNavMenuWriteService constructs a NavMenuWriteService over a NavMenuWriter
// and a read-back NavMenuRepository. The content policy defaults to
// sanitize.New() (never nil / fail closed); pass WithNavMenuContentPolicy to
// share the process-wide policy (Req 3.4, 4).
func NewNavMenuWriteService(w domain.NavMenuWriter, reader domain.NavMenuRepository, opts ...NavMenuWriteOption) *NavMenuWriteService {
	s := &NavMenuWriteService{w: w, reader: reader, policy: sanitize.New()}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// MenuInput is the service's own menu value object (name + optional slug).
type MenuInput struct{ Name, Slug string }

// MenuItemInput is the service's own menu-item value object, mirroring the web
// create DTO before it is sanitized/validated into a domain.NavMenuItemInput.
type MenuItemInput struct {
	Label, URL, Type, Object string
	ObjectID, ParentID       int64
	Order                    int
}

// sanitizeTitle reduces a menu name or item label to plain text (Req 4.1, 4.2).
// It is the single title-sanitization path the service uses for both CreateMenu
// / CreateItem and the partial UpdateMenu / UpdateItem, so all four share one
// behavior.
func (s *NavMenuWriteService) sanitizeTitle(principal auth.Principal, in string) (string, error) {
	return s.policy.Sanitize(sanitize.PostTitle, writerFor(principal), in)
}

// writerFor builds the Policy's Writer argument from the resolved principal
// (Req 4.5): a present principal uses sanitize.For(principal); a zero-value
// (absent) principal uses sanitize.Anonymous(), the fail-closed default. A
// principal is "present" when it carries a non-zero UserID, matching how the
// web layer resolves one from the request context.
func writerFor(principal auth.Principal) sanitize.Writer {
	if principal.UserID == 0 {
		return sanitize.Anonymous()
	}
	return sanitize.For(principal)
}

// CreateMenu sanitizes the name (PostTitle plain-text reduction), derives and
// uniquifies the slug, inserts the menu via the writer, and re-reads it through
// the reader so the handler can emit the read-path DTO (Req 1.1, 1.5, 4.2).
func (s *NavMenuWriteService) CreateMenu(ctx context.Context, principal auth.Principal, in MenuInput) (domain.NavMenu, error) {
	name, err := s.sanitizeTitle(principal, in.Name)
	if err != nil {
		return domain.NavMenu{}, err // fail closed -> 400
	}
	slug, err := s.resolveSlug(ctx, in.Slug, name)
	if err != nil {
		return domain.NavMenu{}, err
	}
	id, err := s.w.CreateMenu(ctx, name, slug)
	if err != nil {
		return domain.NavMenu{}, err
	}
	return s.reader.MenuByID(ctx, id)
}

// UpdateMenu applies a partial update to a menu: only the supplied fields are
// sanitized/normalized and carried into the patch, so an unsupplied field is
// left unchanged (Req 1.2). A missing menu surfaces domain.ErrNotFound from the
// writer. On success it re-reads the menu through the reader.
func (s *NavMenuWriteService) UpdateMenu(ctx context.Context, principal auth.Principal, id int64, in MenuPatchInput) (domain.NavMenu, error) {
	var patch domain.NavMenuPatch
	if in.Name != nil {
		name, err := s.sanitizeTitle(principal, *in.Name)
		if err != nil {
			return domain.NavMenu{}, err
		}
		patch.Name = &name
	}
	if in.Slug != nil {
		slug, err := s.resolveSlug(ctx, *in.Slug, "")
		if err != nil {
			return domain.NavMenu{}, err
		}
		patch.Slug = &slug
	}
	if err := s.w.UpdateMenu(ctx, id, patch); err != nil {
		return domain.NavMenu{}, err
	}
	return s.reader.MenuByID(ctx, id)
}

// DeleteMenu removes a menu and its clean teardown through the writer. It
// returns no body (Req 1.3); a missing menu surfaces domain.ErrNotFound.
func (s *NavMenuWriteService) DeleteMenu(ctx context.Context, _ auth.Principal, id int64) error {
	return s.w.DeleteMenu(ctx, id)
}

// CreateItem vets the item (sanitize label, validate url/type/parent), inserts
// it via the writer, and re-reads the owning menu (Req 2.1, 2.5). Every vet
// failure returns before the writer is called (fail closed, Req 9.3).
func (s *NavMenuWriteService) CreateItem(ctx context.Context, principal auth.Principal, menuID int64, in MenuItemInput) (domain.NavMenu, error) {
	item, err := s.vetItem(ctx, principal, menuID, in)
	if err != nil {
		return domain.NavMenu{}, err
	}
	if _, err := s.w.CreateItem(ctx, menuID, item); err != nil {
		return domain.NavMenu{}, err
	}
	return s.reader.MenuByID(ctx, menuID)
}

// UpdateItem applies a partial update to a menu item: only the supplied fields
// are sanitized/validated and carried into the patch (Req 2.2). A supplied type
// is validated against the accepted set, a supplied custom URL is validated, and
// a supplied non-zero parent is checked for same-menu membership — each failing
// closed before the writer. On success it re-reads the owning menu.
func (s *NavMenuWriteService) UpdateItem(ctx context.Context, principal auth.Principal, menuID, itemID int64, in MenuItemPatchInput) (domain.NavMenu, error) {
	var patch domain.NavMenuItemPatch
	if in.Label != nil {
		label, err := s.sanitizeTitle(principal, *in.Label)
		if err != nil {
			return domain.NavMenu{}, err
		}
		patch.Label = &label
	}
	if in.Type != nil {
		if !validMenuItemType(*in.Type) {
			return domain.NavMenu{}, ErrUnknownMenuItemType
		}
		patch.Type = in.Type
	}
	if in.URL != nil {
		// A URL is validated as a custom URL whenever it is supplied; the stored
		// type governs whether the reader reads it back.
		clean, err := validateCustomURL(*in.URL)
		if err != nil {
			return domain.NavMenu{}, err
		}
		patch.URL = &clean
	}
	if in.Object != nil {
		patch.Object = in.Object
	}
	if in.ObjectID != nil {
		patch.ObjectID = in.ObjectID
	}
	if in.ParentID != nil {
		if *in.ParentID != 0 {
			ok, err := s.w.ItemBelongsToMenu(ctx, menuID, *in.ParentID)
			if err != nil {
				return domain.NavMenu{}, err
			}
			if !ok {
				return domain.NavMenu{}, ErrInvalidMenuParent
			}
		}
		patch.ParentID = in.ParentID
	}
	if in.Order != nil {
		patch.Order = in.Order
	}
	if err := s.w.UpdateItem(ctx, menuID, itemID, patch); err != nil {
		return domain.NavMenu{}, err
	}
	return s.reader.MenuByID(ctx, menuID)
}

// DeleteItem removes a menu item through the writer. It returns no body
// (Req 2.3); a missing item surfaces domain.ErrNotFound.
func (s *NavMenuWriteService) DeleteItem(ctx context.Context, _ auth.Principal, menuID, itemID int64) error {
	return s.w.DeleteItem(ctx, menuID, itemID)
}

// MenuPatchInput is the service-layer partial for UpdateMenu (a nil field is
// left unchanged). The web layer decodes its DTO into this shape.
type MenuPatchInput struct {
	Name *string
	Slug *string
}

// MenuItemPatchInput is the service-layer partial for UpdateItem (a nil field is
// left unchanged).
type MenuItemPatchInput struct {
	Label    *string
	URL      *string
	Type     *string
	Object   *string
	ObjectID *int64
	ParentID *int64
	Order    *int
}

// vetItem sanitizes the label (PostTitle), validates the type and — for custom —
// the URL, and validates a non-zero parent via ItemBelongsToMenu, returning a
// fully-specified domain.NavMenuItemInput only when every check passes. Any
// failure returns before the item is constructed, so the caller never reaches
// the writer (fail closed, Req 4, 5.4, 6.2, 9.3).
func (s *NavMenuWriteService) vetItem(ctx context.Context, principal auth.Principal, menuID int64, in MenuItemInput) (domain.NavMenuItemInput, error) {
	label, err := s.sanitizeTitle(principal, in.Label)
	if err != nil {
		return domain.NavMenuItemInput{}, err
	}
	if !validMenuItemType(in.Type) {
		return domain.NavMenuItemInput{}, ErrUnknownMenuItemType
	}
	outURL := in.URL
	if in.Type == "custom" {
		clean, err := validateCustomURL(in.URL)
		if err != nil {
			return domain.NavMenuItemInput{}, err
		}
		outURL = clean
	}
	if in.ParentID != 0 {
		ok, err := s.w.ItemBelongsToMenu(ctx, menuID, in.ParentID)
		if err != nil {
			return domain.NavMenuItemInput{}, err
		}
		if !ok {
			return domain.NavMenuItemInput{}, ErrInvalidMenuParent
		}
	}
	return domain.NavMenuItemInput{
		Label:    label,
		URL:      outURL,
		Type:     in.Type,
		Object:   in.Object,
		ObjectID: in.ObjectID,
		ParentID: in.ParentID,
		Order:    in.Order,
	}, nil
}

// validMenuItemType reports whether t is one of the accepted _menu_item_type
// values (resolved open question 3, Req 6.2).
func validMenuItemType(t string) bool {
	switch t {
	case "custom", "post_type", "taxonomy":
		return true
	default:
		return false
	}
}

// validateCustomURL parses and vets a custom menu item URL (Req 4.3). It accepts
// a relative path (href="/local/page", which the read path renders verbatim) and
// an absolute URL whose scheme is in the acceptable set; it rejects anything that
// fails to parse or carries a non-acceptable scheme (javascript:, data:,
// vbscript:, …) rather than storing un-vetted markup. The returned string is the
// escaped, re-serialized form persisted to _menu_item_url.
func validateCustomURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", errInvalidMenuURL // -> 400 badRequestError at the handler
	}
	if u.Scheme == "" { // relative URL (kses-style "/local/page"): accept
		return u.String(), nil
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "mailto", "tel", "ftp": // acceptable schemes
		return u.String(), nil
	default:
		return "", errInvalidMenuURL
	}
}

// slugSpaceUnderscore collapses runs of whitespace and underscores to a hyphen.
var slugSpaceUnderscore = regexp.MustCompile(`[\s_]+`)

// slugStrip removes characters outside [a-z0-9-].
var slugStrip = regexp.MustCompile(`[^a-z0-9-]+`)

// slugCollapse collapses runs of two or more hyphens to one.
var slugCollapse = regexp.MustCompile(`-{2,}`)

// sanitizeTitleSlug mirrors WordPress sanitize_title: lowercase; spaces and
// underscores -> hyphens; strip characters that are not [a-z0-9-]; collapse
// repeated hyphens; trim leading/trailing hyphens.
func sanitizeTitleSlug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugSpaceUnderscore.ReplaceAllString(s, "-")
	s = slugStrip.ReplaceAllString(s, "")
	s = slugCollapse.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// resolveSlug derives-or-normalizes a slug then uniquifies it against existing
// nav_menu terms. If the caller supplied a slug, it is normalized; otherwise it
// is derived from name; an empty result falls back to the stable "menu". It then
// appends -2, -3, … until the slug is unique (reader.MenuBySlug returns a
// zero-value NavMenu{} — ID == 0 — for a miss). The writer re-checks uniqueness
// inside its CreateMenu transaction to close the create/create race.
func (s *NavMenuWriteService) resolveSlug(ctx context.Context, supplied, name string) (string, error) {
	base := sanitizeTitleSlug(supplied)
	if base == "" {
		base = sanitizeTitleSlug(name)
	}
	if base == "" {
		base = "menu"
	}
	candidate := base
	for n := 2; ; n++ {
		existing, err := s.reader.MenuBySlug(ctx, candidate)
		if err != nil {
			return "", err
		}
		if existing.ID == 0 { // MenuBySlug returns zero-value NavMenu{} for a miss
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%d", base, n)
	}
}
