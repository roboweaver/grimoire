package config

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ContentConfig is the optional `content:` section declaring custom post types
// and taxonomies. Unlike every other config section, unknown keys inside it are
// a load error (see UnmarshalYAML) so a misspelt key cannot silently change
// routing or capabilities.
type ContentConfig struct {
	PostTypes  []PostTypeConfig `yaml:"post_types"`
	Taxonomies []TaxonomyConfig `yaml:"taxonomies"`
}

// PostTypeConfig declares one custom post type.
type PostTypeConfig struct {
	Name           string     `yaml:"name"`
	Public         *bool      `yaml:"public"`
	RESTBase       string     `yaml:"rest_base"`
	HasArchive     HasArchive `yaml:"has_archive"`
	Slug           string     `yaml:"slug"`
	WithFront      *bool      `yaml:"with_front"`
	Hierarchical   bool       `yaml:"hierarchical"`
	CapabilityType string     `yaml:"capability_type"`
	Taxonomies     []string   `yaml:"taxonomies"`
}

// TaxonomyConfig declares one custom taxonomy.
type TaxonomyConfig struct {
	Name         string   `yaml:"name"`
	Public       *bool    `yaml:"public"`
	RESTBase     string   `yaml:"rest_base"`
	Slug         string   `yaml:"slug"`
	WithFront    *bool    `yaml:"with_front"`
	Hierarchical bool     `yaml:"hierarchical"`
	ObjectTypes  []string `yaml:"object_types"`
}

// HasArchive is WordPress's has_archive: false/omitted (zero value), true, or a
// non-empty custom archive slug. It is comparable with ==.
type HasArchive struct {
	Enabled bool
	Slug    string
}

// UnmarshalYAML decodes the content section strictly: any key not declared on
// the config types is an error naming the field.
func (c *ContentConfig) UnmarshalYAML(node *yaml.Node) error {
	raw, err := yaml.Marshal(node)
	if err != nil {
		return fmt.Errorf("content: re-encode: %w", err)
	}
	type plain ContentConfig // drops this method to avoid recursion
	var p plain
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("content: %w", err)
	}
	*c = ContentConfig(p)
	return nil
}

// UnmarshalYAML accepts a boolean scalar or a non-empty string slug. Strings
// that read as booleans (yes/no/on/off/y/n/true/false, any case) are rejected
// as ambiguous rather than silently treated as slugs.
func (h *HasArchive) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: has_archive must be a boolean or a non-empty string slug", node.Line)
	}
	switch node.Tag {
	case "!!bool":
		var b bool
		if err := node.Decode(&b); err != nil {
			return err
		}
		*h = HasArchive{Enabled: b}
		return nil
	case "!!str":
		s := node.Value
		if s == "" {
			return fmt.Errorf("line %d: has_archive string slug must not be empty", node.Line)
		}
		switch strings.ToLower(s) {
		case "yes", "no", "on", "off", "y", "n", "true", "false":
			return fmt.Errorf("line %d: has_archive %q is ambiguous; use an unquoted true/false or a slug", node.Line, s)
		}
		*h = HasArchive{Enabled: true, Slug: s}
		return nil
	case "!!null":
		*h = HasArchive{}
		return nil
	}
	return fmt.Errorf("line %d: has_archive must be a boolean or a non-empty string slug", node.Line)
}

// applyDefaults fills scalar defaults only. Name-derived defaults are skipped
// for an empty Name so validation can report the missing name.
func (c *ContentConfig) applyDefaults() {
	for i := range c.PostTypes {
		pt := &c.PostTypes[i]
		pt.Public = defaultTrue(pt.Public)
		pt.WithFront = defaultTrue(pt.WithFront)
		if pt.RESTBase == "" {
			pt.RESTBase = pt.Name
		}
		if pt.Slug == "" {
			pt.Slug = pt.Name
		}
		if pt.CapabilityType == "" {
			pt.CapabilityType = "post"
		}
	}
	for i := range c.Taxonomies {
		tx := &c.Taxonomies[i]
		tx.Public = defaultTrue(tx.Public)
		tx.WithFront = defaultTrue(tx.WithFront)
		if tx.RESTBase == "" {
			tx.RESTBase = tx.Name
		}
		if tx.Slug == "" {
			tx.Slug = tx.Name
		}
	}
}

func defaultTrue(p *bool) *bool {
	if p != nil {
		return p
	}
	t := true
	return &t
}
