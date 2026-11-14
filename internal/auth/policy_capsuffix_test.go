package auth

import "testing"

// TestCapabilitySuffix pins WordPress's string-form capability suffix rule:
// the capability type plus "s", with no irregular plurals, and "posts" for the
// empty type (Req 8.2).
func TestCapabilitySuffix(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", "posts"},
		{"post", "posts"},
		{"page", "pages"},
		{"event", "events"},
		{"story", "storys"},
	} {
		if got := CapabilitySuffix(tc.in); got != tc.want {
			t.Errorf("CapabilitySuffix(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCapSuffixUnchangedForExistingTypes re-asserts the existing
// CanCreatePost/CanEditPost/CanDeletePost vectors for post, page and the
// unknown-type default (which uses the posts family), proving capSuffix is
// byte-identical after it is rewritten on top of CapabilitySuffix (Req 8.3).
func TestCapSuffixUnchangedForExistingTypes(t *testing.T) {
	const me, other = int64(1), int64(999)

	type row struct {
		role   string
		op     string // create|edit|delete
		typ    string
		status string
		author int64
		want   bool
	}
	rows := []row{
		// post
		{RoleEditor, "create", "post", "publish", me, true},
		{RoleEditor, "edit", "post", "publish", other, true},
		{RoleEditor, "delete", "post", "publish", other, true},
		{RoleAuthor, "edit", "post", "publish", me, true},
		{RoleAuthor, "edit", "post", "draft", other, false},
		{RoleContributor, "create", "post", "publish", me, false},
		{RoleContributor, "edit", "post", "publish", me, false},
		{RoleSubscriber, "create", "post", "draft", me, false},

		// page
		{RoleAdministrator, "edit", "page", "publish", other, true},
		{RoleAdministrator, "delete", "page", "publish", other, true},
		{RoleEditor, "edit", "page", "publish", other, true},
		{RoleAuthor, "create", "page", "draft", me, false},
		{RoleAuthor, "edit", "page", "draft", me, false},
		{RoleAuthor, "delete", "page", "draft", me, false},

		// unknown type falls back to the posts family
		{RoleAuthor, "create", "widget", "publish", me, true},
		{RoleAuthor, "edit", "widget", "publish", me, true},
		{RoleAuthor, "delete", "widget", "publish", me, true},
		{RoleAuthor, "edit", "widget", "draft", other, false},
		{RoleContributor, "create", "widget", "publish", me, false},
		{RoleEditor, "edit", "widget", "publish", other, true},
		{RoleSubscriber, "edit", "widget", "draft", me, false},
	}

	for _, r := range rows {
		actor := principalFor(r.role)
		var got bool
		switch r.op {
		case "create":
			got = CanCreatePost(actor, r.typ, r.status, r.author)
		case "edit":
			got = CanEditPost(actor, r.typ, r.status, r.author)
		case "delete":
			got = CanDeletePost(actor, r.typ, r.status, r.author)
		default:
			t.Fatalf("unknown op %q", r.op)
		}
		if got != r.want {
			t.Errorf("%s %s %s/%s author=%d: got %v, want %v",
				r.role, r.op, r.typ, r.status, r.author, got, r.want)
		}
	}
}
