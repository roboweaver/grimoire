package wprepo

import "github.com/roboweaver/grimoire/internal/storage/rebind"

// recountStatement returns the one statement that recomputes
// term_taxonomy.count: the number of term_relationships rows of the term
// taxonomy, minus those whose object is a post in the trash status. A
// relationship whose object is not a post (a link_category row) matches no
// posts row and is counted. Both placeholders take the term_taxonomy_id. The
// subquery reads posts while the statement writes term_taxonomy, so MySQL's
// same-table restriction does not apply.
func recountStatement(prefix, vendor string) string {
	return "UPDATE " + prefix + "term_taxonomy SET count = (" +
		"SELECT COUNT(*) FROM " + prefix + "term_relationships tr " +
		"WHERE tr.term_taxonomy_id = ? " +
		"AND NOT EXISTS (SELECT 1 FROM " + prefix + "posts tp " +
		"WHERE tp." + rebind.Ident(vendor, "ID") + " = tr.object_id AND tp.post_status = 'trash')" +
		") WHERE term_taxonomy_id = ?"
}
