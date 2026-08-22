package auth

// This file implements the content-write authorization policy: pure functions
// that decide whether a Principal may create, edit, or delete a post/page, and
// whether it may manage terms, options, or users. WordPress derives these from
// per-post-type meta capabilities; grimoire encodes the same rules directly so
// the write services in internal/content stay thin.

// capSuffix returns the capability suffix for a post type: pages use the
// "*_pages" family, everything else the "*_posts" family.
func capSuffix(postType string) string {
	if postType == "page" {
		return "pages"
	}
	return "posts"
}

// CanCreatePost reports whether actor may create a post of the given type and
// status authored by authorID. Creating on another user's behalf requires the
// edit_others_* capability; publishing requires publish_*.
func CanCreatePost(actor Principal, postType, status string, authorID int64) bool {
	suf := capSuffix(postType)
	if !actor.Can("edit_" + suf) {
		return false
	}
	if authorID != actor.UserID && !actor.Can("edit_others_"+suf) {
		return false
	}
	if status == "publish" && !actor.Can("publish_"+suf) {
		return false
	}
	return true
}

// CanEditPost reports whether actor may edit an existing post of the given type,
// status, and author. Editing another's post requires edit_others_*; editing a
// published post requires edit_published_*; editing another's private post
// requires edit_private_*.
func CanEditPost(actor Principal, postType, status string, authorID int64) bool {
	suf := capSuffix(postType)
	if authorID == actor.UserID {
		if !actor.Can("edit_" + suf) {
			return false
		}
	} else if !actor.Can("edit_others_" + suf) {
		return false
	}
	if status == "publish" && !actor.Can("edit_published_"+suf) {
		return false
	}
	if status == "private" && authorID != actor.UserID && !actor.Can("edit_private_"+suf) {
		return false
	}
	return true
}

// CanDeletePost reports whether actor may delete an existing post of the given
// type, status, and author. It mirrors CanEditPost with the delete_* family.
func CanDeletePost(actor Principal, postType, status string, authorID int64) bool {
	suf := capSuffix(postType)
	if authorID == actor.UserID {
		if !actor.Can("delete_" + suf) {
			return false
		}
	} else if !actor.Can("delete_others_" + suf) {
		return false
	}
	if status == "publish" && !actor.Can("delete_published_"+suf) {
		return false
	}
	if status == "private" && authorID != actor.UserID && !actor.Can("delete_private_"+suf) {
		return false
	}
	return true
}

// CanManageTerms reports whether actor may create or delete taxonomy terms.
func CanManageTerms(actor Principal) bool { return actor.Can("manage_categories") }

// CanManageOptions reports whether actor may read/write site options.
func CanManageOptions(actor Principal) bool { return actor.Can("manage_options") }

// CanCreateUsers reports whether actor may create users.
func CanCreateUsers(actor Principal) bool { return actor.Can("create_users") }

// CanEditUsers reports whether actor may edit users (including roles/passwords).
func CanEditUsers(actor Principal) bool { return actor.Can("edit_users") }

// CanListUsers reports whether actor may list users.
func CanListUsers(actor Principal) bool { return actor.Can("list_users") }

// CanDeleteUsers reports whether actor may delete users.
func CanDeleteUsers(actor Principal) bool { return actor.Can("delete_users") }
