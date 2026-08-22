package content

import (
	"context"
	"errors"

	"github.com/roboweaver/grimoire/internal/domain"
)

// Well-known option names used to assemble the public site chrome.
const (
	OptionBlogName        = "blogname"
	OptionBlogDescription = "blogdescription"
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
// each empty when unset.
func (s *OptionService) SiteInfo(ctx context.Context) (title, tagline string) {
	return s.Get(ctx, OptionBlogName), s.Get(ctx, OptionBlogDescription)
}

// isNotFound reports whether err is the domain not-found sentinel.
func isNotFound(err error) bool { return errors.Is(err, domain.ErrNotFound) }
