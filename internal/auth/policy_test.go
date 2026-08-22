package auth

import "testing"

// principalFor builds a principal holding exactly the given role's capabilities.
func principalFor(role string) Principal {
	return NewPrincipal(1, "u", []string{role})
}

// TestPostPolicyMatrix exercises the create/edit/delete capability rules for the
// five default roles against their own and others' posts, in draft and published
// states. actorID is always 1; "others" posts use author 999.
func TestPostPolicyMatrix(t *testing.T) {
	const me, other = int64(1), int64(999)

	type row struct {
		role   string
		op     string // create|edit|delete
		typ    string // post|page
		status string
		author int64
		want   bool
	}
	rows := []row{
		// Administrator: everything, incl. others' published pages.
		{RoleAdministrator, "create", "post", "publish", me, true},
		{RoleAdministrator, "edit", "page", "publish", other, true},
		{RoleAdministrator, "delete", "page", "publish", other, true},

		// Editor: full control over posts AND pages, own and others'.
		{RoleEditor, "create", "post", "publish", me, true},
		{RoleEditor, "edit", "post", "publish", other, true},
		{RoleEditor, "delete", "post", "publish", other, true},
		{RoleEditor, "edit", "page", "publish", other, true},
		{RoleEditor, "edit", "post", "private", other, true},

		// Author: own posts only; may publish own; cannot touch others'; no pages.
		{RoleAuthor, "create", "post", "publish", me, true},
		{RoleAuthor, "edit", "post", "publish", me, true},
		{RoleAuthor, "delete", "post", "publish", me, true},
		{RoleAuthor, "edit", "post", "draft", other, false},
		{RoleAuthor, "create", "post", "publish", other, false},
		{RoleAuthor, "create", "page", "draft", me, false},

		// Contributor: own drafts only; cannot publish; cannot edit once published.
		{RoleContributor, "create", "post", "draft", me, true},
		{RoleContributor, "edit", "post", "draft", me, true},
		{RoleContributor, "delete", "post", "draft", me, true},
		{RoleContributor, "create", "post", "publish", me, false},
		{RoleContributor, "edit", "post", "publish", me, false},
		{RoleContributor, "delete", "post", "publish", me, false},

		// Subscriber: nothing.
		{RoleSubscriber, "create", "post", "draft", me, false},
		{RoleSubscriber, "edit", "post", "draft", me, false},
		{RoleSubscriber, "delete", "post", "draft", me, false},
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

func TestTermAndOptionPolicy(t *testing.T) {
	if !CanManageTerms(principalFor(RoleEditor)) {
		t.Errorf("editor should manage terms")
	}
	if CanManageTerms(principalFor(RoleAuthor)) {
		t.Errorf("author should not manage terms")
	}
	if !CanManageOptions(principalFor(RoleAdministrator)) {
		t.Errorf("administrator should manage options")
	}
	if CanManageOptions(principalFor(RoleEditor)) {
		t.Errorf("editor should not manage options")
	}
}

func TestUserPolicy(t *testing.T) {
	admin := principalFor(RoleAdministrator)
	editor := principalFor(RoleEditor)
	for _, tc := range []struct {
		name string
		fn   func(Principal) bool
	}{
		{"create", CanCreateUsers},
		{"edit", CanEditUsers},
		{"list", CanListUsers},
		{"delete", CanDeleteUsers},
	} {
		if !tc.fn(admin) {
			t.Errorf("administrator should be able to %s users", tc.name)
		}
		if tc.fn(editor) {
			t.Errorf("editor should not be able to %s users", tc.name)
		}
	}
}
