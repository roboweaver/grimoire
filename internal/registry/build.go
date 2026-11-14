package registry

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/routing"
)

var (
	nameRE = regexp.MustCompile(`^[a-z0-9_-]+$`)
	slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

const (
	maxPostTypeName = 20
	maxTaxonomyName = 32
)

// claim is one resolved URL segment sequence owned by one public entry.
type claim struct {
	seq   []string
	owner int // unique per entry across post types and taxonomies
}

type builder struct {
	st       routing.Structure
	errs     []error
	claims   []claim
	restSeen map[string]bool
}

func (b *builder) errf(format string, a ...any) {
	b.errs = append(b.errs, fmt.Errorf(format, a...))
}

// overlaps reports whether one sequence is a prefix of, or equal to, the other.
func overlaps(a, c []string) bool {
	n := min(len(a), len(c))
	return slices.Equal(a[:n], c[:n])
}

func deref(p *bool) bool { return p == nil || *p }

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// Build validates cfg against the permalink structure (rules R1-R8 of the
// design) and returns the registry, or the errors.Join of every violation.
// Build never consults the database (R8).
func Build(cfg config.ContentConfig, st routing.Structure) (*Registry, error) {
	b := &builder{st: st, restSeen: map[string]bool{}}

	ptNames := map[string]bool{}
	txNames := map[string]bool{}
	for _, p := range cfg.PostTypes {
		ptNames[p.Name] = true
	}
	for _, t := range cfg.Taxonomies {
		txNames[t.Name] = true
	}

	reg := &Registry{
		postTypeByName: map[string]int{},
		taxonomyByName: map[string]int{},
		postTypeByREST: map[string]int{},
		taxonomyByREST: map[string]int{},
	}

	owner := 0
	seenPT := map[string]bool{}
	for i, c := range cfg.PostTypes {
		path := fmt.Sprintf("content.post_types[%d]", i)
		b.checkName(path, c.Name, maxPostTypeName, reservedPostTypes, seenPT)
		pt := PostType{
			Name:           c.Name,
			Slug:           orDefault(c.Slug, c.Name),
			RESTBase:       orDefault(c.RESTBase, c.Name),
			Public:         deref(c.Public),
			WithFront:      deref(c.WithFront),
			Hierarchical:   c.Hierarchical,
			CapabilityType: orDefault(c.CapabilityType, "post"),
			Taxonomies:     slices.Clone(c.Taxonomies),
		}
		if c.HasArchive.Enabled {
			pt.ArchiveSlug = orDefault(c.HasArchive.Slug, pt.Slug)
		}
		if !nameRE.MatchString(pt.CapabilityType) {
			b.errf("%s.capability_type: %q must match %s", path, pt.CapabilityType, nameRE)
		}
		if pt.Public {
			b.checkBase(path+".slug", pt.Slug, pt.WithFront, owner)
			if c.HasArchive.Enabled && c.HasArchive.Slug != "" {
				b.checkBase(path+".has_archive", pt.ArchiveSlug, pt.WithFront, owner)
			}
		}
		b.checkREST(path, pt.RESTBase, false)
		for _, n := range c.Taxonomies {
			if n != "category" && n != "post_tag" && !txNames[n] {
				b.errf("%s.taxonomies: %q is not a registered taxonomy, category or post_tag", path, n)
			}
		}
		reg.postTypes = append(reg.postTypes, pt)
		owner++
	}

	seenTX := map[string]bool{}
	for i, c := range cfg.Taxonomies {
		path := fmt.Sprintf("content.taxonomies[%d]", i)
		b.checkName(path, c.Name, maxTaxonomyName, reservedTaxonomies, seenTX)
		tx := Taxonomy{
			Name:         c.Name,
			Slug:         orDefault(c.Slug, c.Name),
			RESTBase:     orDefault(c.RESTBase, c.Name),
			Public:       deref(c.Public),
			WithFront:    deref(c.WithFront),
			Hierarchical: c.Hierarchical,
			ObjectTypes:  slices.Clone(c.ObjectTypes),
		}
		if tx.Public {
			b.checkBase(path+".slug", tx.Slug, tx.WithFront, owner)
		}
		b.checkREST(path, tx.RESTBase, true)
		for _, n := range c.ObjectTypes {
			if n != "post" && n != "page" && !ptNames[n] {
				b.errf("%s.object_types: %q is not a registered post type, post or page", path, n)
			}
		}
		reg.taxonomies = append(reg.taxonomies, tx)
		owner++
	}

	if len(b.errs) > 0 {
		return nil, errors.Join(b.errs...)
	}

	reg.applyUnion()
	for i, p := range reg.postTypes {
		reg.postTypeByName[p.Name] = i
		reg.postTypeByREST[p.RESTBase] = i
	}
	for i, t := range reg.taxonomies {
		reg.taxonomyByName[t.Name] = i
		reg.taxonomyByREST[t.RESTBase] = i
	}
	return reg, nil
}

// applyUnion makes each side the bidirectional union of both declarations,
// deduplicated, declared entries first then the other side in declaration order.
func (r *Registry) applyUnion() {
	for i := range r.postTypes {
		p := &r.postTypes[i]
		p.Taxonomies = dedup(p.Taxonomies)
		for _, t := range r.taxonomies {
			if slices.Contains(t.ObjectTypes, p.Name) && !slices.Contains(p.Taxonomies, t.Name) {
				p.Taxonomies = append(p.Taxonomies, t.Name)
			}
		}
	}
	for i := range r.taxonomies {
		t := &r.taxonomies[i]
		t.ObjectTypes = dedup(t.ObjectTypes)
		for _, p := range r.postTypes {
			if slices.Contains(p.Taxonomies, t.Name) && !slices.Contains(t.ObjectTypes, p.Name) {
				t.ObjectTypes = append(t.ObjectTypes, p.Name)
			}
		}
	}
}

func dedup(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// checkName applies R1-R4 to one entry name.
func (b *builder) checkName(path, name string, maxLen int, reserved []string, seen map[string]bool) {
	if name == "" {
		b.errf("%s.name: must not be empty", path)
		return
	}
	if !nameRE.MatchString(name) {
		b.errf("%s.name: %q must match %s", path, name, nameRE)
	}
	if len(name) > maxLen {
		b.errf("%s.name: %q is longer than %d characters", path, name, maxLen)
	}
	if slices.Contains(reserved, name) {
		b.errf("%s.name: %q is reserved", path, name)
	}
	if seen[name] {
		b.errf("%s.name: %q is already declared", path, name)
	}
	seen[name] = true
}

// checkBase applies R5 to one public entry's slug or archive slug.
func (b *builder) checkBase(path, slug string, withFront bool, owner int) {
	if !slugRE.MatchString(slug) {
		b.errf("%s: %q must be a single segment matching %s", path, slug, slugRE)
		return
	}
	seq := b.st.CustomBase(slug, withFront)

	if slices.Contains(reservedRoutes, seq[0]) {
		b.errf("%s: %q starts with the reserved route %q", path, slug, seq[0])
		return
	}
	front := b.st.Front()
	if len(front) > 0 && len(seq) <= len(front) && slices.Equal(seq, front[:len(seq)]) {
		b.errf("%s: %q collides with the permalink front %v", path, slug, front)
		return
	}
	for _, rb := range b.st.ReservedBases() {
		if overlaps(seq, rb) {
			b.errf("%s: %q collides with the built-in base %v", path, slug, rb)
			return
		}
	}
	for _, c := range b.claims {
		if c.owner != owner && overlaps(seq, c.seq) {
			b.errf("%s: %q collides with another entry's base %v", path, slug, c.seq)
			return
		}
	}
	b.claims = append(b.claims, claim{seq: seq, owner: owner})
}

// checkREST applies R6 to one entry's rest_base. Private entries still count.
func (b *builder) checkREST(path, base string, taxonomy bool) {
	field := path + ".rest_base"
	if !nameRE.MatchString(base) {
		b.errf("%s: %q must match %s", field, base, nameRE)
		return
	}
	if slices.Contains(reservedRESTBases, base) {
		b.errf("%s: %q is a reserved REST collection", field, base)
	}
	if taxonomy && slices.Contains(restItemCommon, base) {
		b.errf("%s: %q collides with a REST item key", field, base)
	}
	if b.restSeen[base] {
		b.errf("%s: %q is already used by another entry", field, base)
	}
	b.restSeen[base] = true
}
