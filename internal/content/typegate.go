package content

import "github.com/roboweaver/grimoire/internal/registry"

// WritableType reports whether the write services may modify a stored row of
// typ: the built-in types and the empty string always, a registered type when
// reg holds it, nothing else.
func WritableType(reg *registry.Registry, typ string) bool {
	switch typ {
	case "", "post", "page":
		return true
	}
	if reg == nil {
		return false
	}
	_, ok := reg.PostType(typ)
	return ok
}

// ManagedTaxonomy reports whether the term services may write terms of
// taxonomy: the built-in taxonomies always, a registered taxonomy when reg holds
// it, nothing else.
func ManagedTaxonomy(reg *registry.Registry, taxonomy string) bool {
	switch taxonomy {
	case TaxonomyCategory, taxonomyPostTag:
		return true
	}
	if reg == nil {
		return false
	}
	_, ok := reg.Taxonomy(taxonomy)
	return ok
}
