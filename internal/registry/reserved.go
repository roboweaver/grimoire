package registry

import "slices"

var reservedPostTypes = []string{
	"post", "page", "attachment", "revision", "nav_menu_item", "custom_css",
	"customize_changeset", "oembed_cache", "user_request", "wp_block",
	"wp_template", "wp_template_part", "wp_global_styles", "wp_navigation",
}

var reservedTaxonomies = []string{
	"category", "post_tag", "nav_menu", "link_category", "post_format",
}

// reservedRoutes are first path segments registered ahead of the content
// patterns; a custom base starting with one would be unreachable or collide.
var reservedRoutes = []string{
	"admin", "wp-json", "wp-admin", "wp-content", "wp-includes", "assets",
	"theme", "login", "logout", "comment", "healthz", "favicon.ico", "feed",
}

// reservedRESTBases are the core collections served (Req 3.6) or not yet
// implemented, so a custom rest_base cannot shadow them.
var reservedRESTBases = []string{
	"posts", "pages", "media", "comments", "users", "categories", "tags",
	"types", "taxonomies", "statuses", "settings", "search", "blocks", "themes", "plugins",
}

// restItemCommon are the JSON keys every REST item carries; a taxonomy
// rest_base is also an item key, so it must not equal one.
var restItemCommon = []string{
	"id", "date", "date_gmt", "modified", "modified_gmt", "slug", "status", "type",
	"link", "guid", "title", "content", "excerpt", "author", "featured_media",
	"comment_status", "ping_status",
}

// ReservedPostTypes returns the post type names a config entry may not use.
func ReservedPostTypes() []string { return slices.Clone(reservedPostTypes) }

// ReservedTaxonomies returns the taxonomy names a config entry may not use.
func ReservedTaxonomies() []string { return slices.Clone(reservedTaxonomies) }
