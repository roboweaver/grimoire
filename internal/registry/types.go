// Package registry validates the optional `content:` section (custom post types
// and taxonomies) against the permalink structure and holds the result. It is
// pure: it imports only config and routing.
package registry

// PostType is a validated, fully defaulted post type entry.
type PostType struct {
	Name           string
	Slug           string // rewrite slug, single segment
	ArchiveSlug    string // "" when has_archive is false
	RESTBase       string
	Public         bool
	WithFront      bool
	Hierarchical   bool
	CapabilityType string
	// Taxonomies is EFFECTIVE: declared taxonomies plus taxonomies naming this
	// type in object_types, deduplicated, in declaration order.
	Taxonomies []string
}

// Taxonomy is a validated, fully defaulted taxonomy entry.
type Taxonomy struct {
	Name         string
	Slug         string
	RESTBase     string
	Public       bool
	WithFront    bool
	Hierarchical bool
	// ObjectTypes is EFFECTIVE: declared object_types plus post types listing
	// this taxonomy, deduplicated, in declaration order.
	ObjectTypes []string
}

// Registry holds the validated entries in declaration order with name and
// rest_base indexes (positions into the ordered slices).
type Registry struct {
	postTypes  []PostType
	taxonomies []Taxonomy

	postTypeByName map[string]int
	taxonomyByName map[string]int
	postTypeByREST map[string]int
	taxonomyByREST map[string]int
}
