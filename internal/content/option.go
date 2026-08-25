package content

import (
	"context"
	"errors"
	"html"
	"strings"

	"github.com/roboweaver/grimoire/internal/domain"
)

// Well-known option names used to assemble the public site chrome.
const (
	OptionBlogName        = "blogname"
	OptionBlogDescription = "blogdescription"
	OptionSiteURL         = "siteurl"
	OptionHome            = "home"
)

// OptionService reads site options with absent-as-empty semantics so a missing
// option never breaks a page render.
type OptionService struct {
	opts domain.OptionRepository
}

// NewOptionService constructs an OptionService.
func NewOptionService(o domain.OptionRepository) *OptionService {
	return &OptionService{opts: o}
}

// Get returns the option value, or "" when the option is absent or any error
// occurs. Callers that need to distinguish those cases should use GetErr.
func (s *OptionService) Get(ctx context.Context, name string) string {
	v, err := s.opts.Get(ctx, name)
	if err != nil {
		return ""
	}
	return v
}

// GetErr returns the option value and its error. domain.ErrNotFound is mapped
// to ("", nil) so absence is not an error; other errors are surfaced.
func (s *OptionService) GetErr(ctx context.Context, name string) (string, error) {
	v, err := s.opts.Get(ctx, name)
	if err != nil {
		if isNotFound(err) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// SiteInfo returns the site title (blogname) and tagline (blogdescription),
// each empty when unset. WordPress stores these options with any special
// characters already HTML-entity-encoded at rest (e.g. a literal "&#039;"
// for an apostrophe), relying on its own display path to decode them back.
// html.UnescapeString reverses that encoding here so html/template's
// auto-escaper (which only escapes, never decodes) renders the intended
// character instead of the literal entity text.
func (s *OptionService) SiteInfo(ctx context.Context) (title, tagline string) {
	return html.UnescapeString(s.Get(ctx, OptionBlogName)), html.UnescapeString(s.Get(ctx, OptionBlogDescription))
}

// BaseURLs returns the distinct, trailing-slash-trimmed "siteurl" and "home"
// option values, each expanded to both http and https variants. WordPress
// post content commonly embeds these absolute URLs (e.g. pasted media links,
// or content imported/migrated without a search-replace pass), which would
// otherwise force every page load to depend on the original WordPress
// instance being reachable at that address. Callers use this to rewrite such
// absolute self-references to relative paths so grimoire can serve content
// (including its own /wp-content/uploads/* media route) fully standalone.
// Empty/unset options are omitted; the result has no duplicates.
func (s *OptionService) BaseURLs(ctx context.Context) []string {
	seen := make(map[string]struct{}, 4)
	var out []string
	add := func(raw string) {
		raw = strings.TrimRight(strings.TrimSpace(raw), "/")
		if raw == "" {
			return
		}
		variants := []string{raw}
		switch {
		case strings.HasPrefix(raw, "http://"):
			variants = append(variants, "https://"+strings.TrimPrefix(raw, "http://"))
		case strings.HasPrefix(raw, "https://"):
			variants = append(variants, "http://"+strings.TrimPrefix(raw, "https://"))
		}
		for _, v := range variants {
			if _, ok := seen[v]; ok {
				continue
			}
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	add(s.Get(ctx, OptionSiteURL))
	add(s.Get(ctx, OptionHome))
	return out
}

// isNotFound reports whether err is the domain not-found sentinel.
func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
