package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/registry"
)

// reportUndeclaredContentTypes prints, for `migrate -check`, the post types and
// taxonomies that have rows in the database but are neither built in nor
// declared under content: in the config, and any declared type that has no
// rows (M12 Req 11.1-11.4; D2, D9).
//
// It reads names and counts only, through the narrow auditor ports; no title,
// slug or body is touched. Each auditor is called exactly once. Nothing is
// printed when there is nothing to say, for the reason reportDuplicateNicenames
// gives. A read error is reported as a "Could not check" line and swallowed:
// the function has no result, so it cannot change the exit status (Req 11.3).
func reportUndeclaredContentTypes(
	ctx context.Context,
	w io.Writer,
	postTypes domain.PostTypeAuditor,
	taxonomies domain.TaxonomyAuditor,
	declared config.ContentConfig,
) {
	declaredPT := make([]string, 0, len(declared.PostTypes))
	for _, p := range declared.PostTypes {
		declaredPT = append(declaredPT, p.Name)
	}
	declaredTax := make([]string, 0, len(declared.Taxonomies))
	for _, t := range declared.Taxonomies {
		declaredTax = append(declaredTax, t.Name)
	}

	rows, err := postTypes.DistinctPostTypes(ctx)
	if err != nil {
		fmt.Fprintf(w, "Could not check for undeclared post types: %v\n", err)
	} else {
		reportTypeCounts(w, "post type", "content.post_types", rows,
			registry.ReservedPostTypes(), declaredPT)
	}

	rows, err = taxonomies.DistinctTaxonomies(ctx)
	if err != nil {
		fmt.Fprintf(w, "Could not check for undeclared taxonomies: %v\n", err)
	} else {
		reportTypeCounts(w, "taxonomy", "content.taxonomies", rows,
			registry.ReservedTaxonomies(), declaredTax)
	}
}

// reportTypeCounts is the pure subtraction behind the report: rows minus
// reserved minus declared are listed as undeclared, and declared names absent
// from rows are listed as having no rows. Entry lines are indented; headers are not.
func reportTypeCounts(w io.Writer, noun, key string, rows []domain.TypeCount, reserved, declared []string) {
	present := make(map[string]bool, len(rows))
	var undeclared []domain.TypeCount
	for _, r := range rows {
		present[r.Name] = true
		if slices.Contains(reserved, r.Name) || slices.Contains(declared, r.Name) {
			continue
		}
		undeclared = append(undeclared, r)
	}
	if len(undeclared) > 0 {
		fmt.Fprintf(w, "Undeclared %ss found in the database; grimoire does not serve them. "+
			"Add each to %s to serve it:\n", noun, key)
		for _, r := range undeclared {
			fmt.Fprintf(w, "  %q: %d rows\n", r.Name, r.Count)
		}
	}
	var empty []string
	for _, name := range declared {
		if !present[name] {
			empty = append(empty, name)
		}
	}
	if len(empty) > 0 {
		fmt.Fprintf(w, "Declared %s with no rows in the database:\n", plural(noun))
		for _, name := range empty {
			fmt.Fprintf(w, "  %q\n", name)
		}
	}
}

// plural returns the plural of a content-type noun ("post type", "taxonomy").
func plural(noun string) string {
	if strings.HasSuffix(noun, "y") {
		return strings.TrimSuffix(noun, "y") + "ies"
	}
	return noun + "s"
}
