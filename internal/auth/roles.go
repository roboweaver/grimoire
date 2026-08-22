// Package auth provides WordPress-compatible roles and capabilities, principal
// authorization checks, and session management for grimoire.
//
// Roles and capabilities mirror the five default WordPress roles. A user's role
// assignment is stored in usermeta under {prefix}capabilities as a PHP-serialized
// array (role name -> true); the legacy {prefix}user_level integer is derived
// from the highest role. Authorization is a capability check via Principal.Can.
package auth

// The five default WordPress role identifiers.
const (
	RoleAdministrator = "administrator"
	RoleEditor        = "editor"
	RoleAuthor        = "author"
	RoleContributor   = "contributor"
	RoleSubscriber    = "subscriber"
)

// userLevels maps each default role to its legacy numeric user level, matching
// WordPress ({prefix}user_level). Higher is more privileged.
var userLevels = map[string]int{
	RoleAdministrator: 10,
	RoleEditor:        7,
	RoleAuthor:        2,
	RoleContributor:   1,
	RoleSubscriber:    0,
}

// UserLevel returns the highest legacy user level among the given roles. Unknown
// roles contribute 0; calling with no roles returns 0.
func UserLevel(roles ...string) int {
	level := 0
	for _, r := range roles {
		if l, ok := userLevels[r]; ok && l > level {
			level = l
		}
	}
	return level
}

// capabilitySets defines the WordPress-standard capability set granted by each
// default role. Capabilities are additive across roles.
var capabilitySets = map[string][]string{
	RoleSubscriber: {
		"read",
	},
	RoleContributor: {
		"read",
		"edit_posts",
		"delete_posts",
	},
	RoleAuthor: {
		"read",
		"upload_files",
		"edit_posts",
		"edit_published_posts",
		"publish_posts",
		"delete_posts",
		"delete_published_posts",
	},
	RoleEditor: {
		"read",
		"read_private_pages",
		"read_private_posts",
		"unfiltered_html",
		"upload_files",
		"moderate_comments",
		"manage_categories",
		"manage_links",
		"edit_posts",
		"edit_others_posts",
		"edit_published_posts",
		"edit_private_posts",
		"publish_posts",
		"delete_posts",
		"delete_others_posts",
		"delete_published_posts",
		"delete_private_posts",
		"edit_pages",
		"edit_others_pages",
		"edit_published_pages",
		"edit_private_pages",
		"publish_pages",
		"delete_pages",
		"delete_others_pages",
		"delete_published_pages",
		"delete_private_pages",
	},
	RoleAdministrator: {
		"read",
		"read_private_pages",
		"read_private_posts",
		"unfiltered_html",
		"unfiltered_upload",
		"upload_files",
		"moderate_comments",
		"manage_categories",
		"manage_links",
		"manage_options",
		"edit_posts",
		"edit_others_posts",
		"edit_published_posts",
		"edit_private_posts",
		"publish_posts",
		"delete_posts",
		"delete_others_posts",
		"delete_published_posts",
		"delete_private_posts",
		"edit_pages",
		"edit_others_pages",
		"edit_published_pages",
		"edit_private_pages",
		"publish_pages",
		"delete_pages",
		"delete_others_pages",
		"delete_published_pages",
		"delete_private_pages",
		"edit_dashboard",
		"edit_theme_options",
		"switch_themes",
		"install_themes",
		"update_themes",
		"delete_themes",
		"activate_plugins",
		"install_plugins",
		"update_plugins",
		"delete_plugins",
		"import",
		"export",
		"update_core",
		"list_users",
		"create_users",
		"edit_users",
		"delete_users",
		"promote_users",
		"remove_users",
	},
}

// CapabilitiesForRoles returns the union capability set granted by the given
// roles. Unknown roles contribute nothing. The returned map is a fresh copy and
// safe for the caller to mutate.
func CapabilitiesForRoles(roles ...string) map[string]bool {
	caps := make(map[string]bool)
	for _, r := range roles {
		for _, c := range capabilitySets[r] {
			caps[c] = true
		}
	}
	return caps
}
