package auth

import "testing"

// capMatrix documents, per role, a capability it MUST have and one it MUST NOT,
// exercising the WordPress-standard capability boundaries the write API relies on.
func TestCapabilitiesForRoles_matrix(t *testing.T) {
	cases := []struct {
		role  string
		has   []string
		lacks []string
	}{
		{
			role:  RoleAdministrator,
			has:   []string{"manage_options", "edit_users", "edit_others_posts", "publish_posts", "delete_others_pages", "manage_categories"},
			lacks: []string{}, // administrator holds every enumerated capability
		},
		{
			role:  RoleEditor,
			has:   []string{"edit_others_posts", "publish_posts", "delete_others_posts", "edit_others_pages", "manage_categories", "read_private_posts"},
			lacks: []string{"manage_options", "edit_users", "activate_plugins"},
		},
		{
			role:  RoleAuthor,
			has:   []string{"publish_posts", "edit_published_posts", "delete_published_posts", "upload_files"},
			lacks: []string{"edit_others_posts", "manage_categories", "manage_options"},
		},
		{
			role:  RoleContributor,
			has:   []string{"edit_posts", "delete_posts", "read"},
			lacks: []string{"publish_posts", "upload_files", "edit_others_posts"},
		},
		{
			role:  RoleSubscriber,
			has:   []string{"read"},
			lacks: []string{"edit_posts", "publish_posts", "delete_posts"},
		},
	}
	for _, c := range cases {
		caps := CapabilitiesForRoles(c.role)
		for _, cap := range c.has {
			if !caps[cap] {
				t.Errorf("role %q: expected capability %q, missing", c.role, cap)
			}
		}
		for _, cap := range c.lacks {
			if caps[cap] {
				t.Errorf("role %q: unexpected capability %q present", c.role, cap)
			}
		}
	}
}

func TestCapabilitiesForRoles_union(t *testing.T) {
	caps := CapabilitiesForRoles(RoleAuthor, RoleContributor)
	// author contributes publish_posts; contributor contributes nothing author lacks
	if !caps["publish_posts"] {
		t.Errorf("union of author+contributor should include publish_posts")
	}
	if !caps["edit_posts"] {
		t.Errorf("union should include edit_posts")
	}
	if caps["manage_options"] {
		t.Errorf("union of author+contributor must not include manage_options")
	}
}

func TestCapabilitiesForRoles_unknownRoleYieldsNoCaps(t *testing.T) {
	caps := CapabilitiesForRoles("nonesuch")
	if len(caps) != 0 {
		t.Errorf("unknown role should yield no capabilities, got %d", len(caps))
	}
}

func TestUserLevel(t *testing.T) {
	cases := map[string]int{
		RoleAdministrator: 10,
		RoleEditor:        7,
		RoleAuthor:        2,
		RoleContributor:   1,
		RoleSubscriber:    0,
		"nonesuch":        0,
	}
	for role, want := range cases {
		if got := UserLevel(role); got != want {
			t.Errorf("UserLevel(%q) = %d, want %d", role, got, want)
		}
	}
	// UserLevel returns the highest level across a set of roles.
	if got := UserLevel(RoleSubscriber, RoleEditor, RoleContributor); got != 7 {
		t.Errorf("UserLevel(subscriber,editor,contributor) = %d, want 7", got)
	}
	if got := UserLevel(); got != 0 {
		t.Errorf("UserLevel() with no roles = %d, want 0", got)
	}
}
