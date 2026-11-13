package registry

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/roboweaver/grimoire/internal/routing"
)

// HasArchive reports whether the type serves an archive. A private type never
// does, whatever its archive slug says.
func (p PostType) HasArchive() bool { return p.Public && p.ArchiveSlug != "" }

// Heading is the human heading for the type: Name with '-' and '_' replaced by
// spaces and the first rune upper-cased.
func (p PostType) Heading() string {
	s := strings.NewReplacer("-", " ", "_", " ").Replace(p.Name)
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return strings.ToUpper(string(r)) + s[n:]
}

func (p PostType) clone() PostType {
	p.Taxonomies = slices.Clone(p.Taxonomies)
	return p
}

func (t Taxonomy) clone() Taxonomy {
	t.ObjectTypes = slices.Clone(t.ObjectTypes)
	return t
}

// Empty reports whether nothing is registered. A nil registry is empty.
func (r *Registry) Empty() bool {
	return r == nil || (len(r.postTypes) == 0 && len(r.taxonomies) == 0)
}

// PostTypes returns the post types in declaration order (a defensive copy).
func (r *Registry) PostTypes() []PostType {
	if r == nil {
		return nil
	}
	out := make([]PostType, len(r.postTypes))
	for i, p := range r.postTypes {
		out[i] = p.clone()
	}
	return out
}

// Taxonomies returns the taxonomies in declaration order (a defensive copy).
func (r *Registry) Taxonomies() []Taxonomy {
	if r == nil {
		return nil
	}
	out := make([]Taxonomy, len(r.taxonomies))
	for i, t := range r.taxonomies {
		out[i] = t.clone()
	}
	return out
}

// PostType looks a post type up by name.
func (r *Registry) PostType(name string) (PostType, bool) {
	if r == nil {
		return PostType{}, false
	}
	i, ok := r.postTypeByName[name]
	if !ok {
		return PostType{}, false
	}
	return r.postTypes[i].clone(), true
}

// Taxonomy looks a taxonomy up by name.
func (r *Registry) Taxonomy(name string) (Taxonomy, bool) {
	if r == nil {
		return Taxonomy{}, false
	}
	i, ok := r.taxonomyByName[name]
	if !ok {
		return Taxonomy{}, false
	}
	return r.taxonomies[i].clone(), true
}

// PostTypeByRESTBase looks a post type up by its REST collection base.
func (r *Registry) PostTypeByRESTBase(base string) (PostType, bool) {
	if r == nil {
		return PostType{}, false
	}
	i, ok := r.postTypeByREST[base]
	if !ok {
		return PostType{}, false
	}
	return r.postTypes[i].clone(), true
}

// TaxonomyByRESTBase looks a taxonomy up by its REST collection base.
func (r *Registry) TaxonomyByRESTBase(base string) (Taxonomy, bool) {
	if r == nil {
		return Taxonomy{}, false
	}
	i, ok := r.taxonomyByREST[base]
	if !ok {
		return Taxonomy{}, false
	}
	return r.taxonomies[i].clone(), true
}

// TaxonomyRESTBase returns the REST base for a taxonomy name: the built-ins
// category and post_tag, then registered taxonomies. ("", false) when unknown.
func (r *Registry) TaxonomyRESTBase(name string) (string, bool) {
	switch name {
	case "category":
		return "categories", true
	case "post_tag":
		return "tags", true
	}
	if t, ok := r.Taxonomy(name); ok {
		return t.RESTBase, true
	}
	return "", false
}

// Routes projects the PUBLIC entries into routing values, in declaration order.
func (r *Registry) Routes() ([]routing.TypeRoute, []routing.TaxRoute) {
	if r == nil {
		return nil, nil
	}
	var types []routing.TypeRoute
	for _, p := range r.postTypes {
		if !p.Public {
			continue
		}
		tr := routing.TypeRoute{
			Name:         p.Name,
			Slug:         p.Slug,
			WithFront:    p.WithFront,
			Hierarchical: p.Hierarchical,
		}
		if p.HasArchive() {
			tr.ArchiveSlug = p.ArchiveSlug
		}
		types = append(types, tr)
	}
	var taxes []routing.TaxRoute
	for _, t := range r.taxonomies {
		if !t.Public {
			continue
		}
		taxes = append(taxes, routing.TaxRoute{
			Name:         t.Name,
			Slug:         t.Slug,
			WithFront:    t.WithFront,
			Hierarchical: t.Hierarchical,
		})
	}
	return types, taxes
}

// Notes returns non-fatal observations: one per taxonomy attached to no object
// type. Build never fails because of them.
func (r *Registry) Notes() []string {
	if r == nil {
		return nil
	}
	var notes []string
	for _, t := range r.taxonomies {
		if len(t.ObjectTypes) == 0 {
			notes = append(notes, fmt.Sprintf("taxonomy %q is attached to no object type", t.Name))
		}
	}
	return notes
}
