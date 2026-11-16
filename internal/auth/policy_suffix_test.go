package auth

import "testing"

// Tests for the suffix-taking capability functions (Req 7.1, 7.4, 15.3; D2).
// They fail to compile until CanCreatePostSuffix, CanEditPostSuffix and
// CanDeletePostSuffix exist (task 7.2).

const suffixProducts = "products"

var allRoles = []string{RoleAdministrator, RoleEditor, RoleAuthor, RoleContributor, RoleSubscriber}

// productsPrincipal builds principal id 1 holding exactly the given explicit
// capability grants (no role).
func productsPrincipal(caps ...string) Principal {
	return NewPrincipal(1, "u", caps)
}

// TestSuffixRolesHoldNoProductsCaps: no default role carries any *_products
// capability, so every role is denied every verb for "products", including
// roles that fully control posts (the Finding 2 case: edit_posts is not enough).
func TestSuffixRolesHoldNoProductsCaps(t *testing.T) {
	const me, other = int64(1), int64(999)
	for _, role := range allRoles {
		a := principalFor(role)
		for _, status := range []string{"draft", "publish", "private"} {
			for _, author := range []int64{me, other} {
				if CanCreatePostSuffix(a, suffixProducts, status, author) {
					t.Errorf("%s create products/%s author=%d: want false", role, status, author)
				}
				if CanEditPostSuffix(a, suffixProducts, status, author) {
					t.Errorf("%s edit products/%s author=%d: want false", role, status, author)
				}
				if CanDeletePostSuffix(a, suffixProducts, status, author) {
					t.Errorf("%s delete products/%s author=%d: want false", role, status, author)
				}
			}
		}
	}
}

// TestSuffixEditPostsAloneIsNotEnough: an author-role principal (edit_posts,
// publish_posts, ...) with no *_products grant cannot write products; granting
// edit_products flips only the drafts-by-self case.
func TestSuffixEditPostsAloneIsNotEnough(t *testing.T) {
	a := NewPrincipal(1, "u", []string{RoleAuthor})
	if !a.Can("edit_posts") {
		t.Fatal("precondition: author must hold edit_posts")
	}
	if CanCreatePostSuffix(a, suffixProducts, "draft", 1) {
		t.Error("create with edit_posts only: want false")
	}
	if CanEditPostSuffix(a, suffixProducts, "draft", 1) {
		t.Error("edit with edit_posts only: want false")
	}
	if CanDeletePostSuffix(a, suffixProducts, "draft", 1) {
		t.Error("delete with edit_posts only: want false")
	}
	a2 := NewPrincipal(1, "u", []string{RoleAuthor, "edit_products"})
	if !CanCreatePostSuffix(a2, suffixProducts, "draft", 1) {
		t.Error("create with edit_products: want true")
	}
	if !CanEditPostSuffix(a2, suffixProducts, "draft", 1) {
		t.Error("edit own draft with edit_products: want true")
	}
	if CanDeletePostSuffix(a2, suffixProducts, "draft", 1) {
		t.Error("delete with edit_products but no delete_products: want false")
	}
}

// TestSuffixProductsMatrix drives the same decision matrix as the post tests
// through the *_products family using explicit grants.
func TestSuffixProductsMatrix(t *testing.T) {
	const me, other = int64(1), int64(999)
	type row struct {
		name   string
		caps   []string
		op     string // create|edit|delete
		status string
		author int64
		want   bool
	}
	rows := []row{
		// no grants
		{"none create", nil, "create", "draft", me, false},
		{"none edit", nil, "edit", "draft", me, false},
		{"none delete", nil, "delete", "draft", me, false},

		// create
		{"create draft", []string{"edit_products"}, "create", "draft", me, true},
		{"create publish needs publish_", []string{"edit_products"}, "create", "publish", me, false},
		{"create publish granted", []string{"edit_products", "publish_products"}, "create", "publish", me, true},
		{"create private no publish needed", []string{"edit_products"}, "create", "private", me, true},
		{"create for other needs edit_others_", []string{"edit_products"}, "create", "draft", other, false},
		{"create for other granted", []string{"edit_products", "edit_others_products"}, "create", "draft", other, true},
		{"create publish for other needs both", []string{"edit_products", "edit_others_products"}, "create", "publish", other, false},
		{"publish_ without edit_", []string{"publish_products"}, "create", "publish", me, false},
		{"post-family grants ignored", []string{"edit_posts", "publish_posts", "edit_others_posts"}, "create", "draft", me, false},

		// edit
		{"edit own draft", []string{"edit_products"}, "edit", "draft", me, true},
		{"edit own published needs edit_published_", []string{"edit_products"}, "edit", "publish", me, false},
		{"edit own published granted", []string{"edit_products", "edit_published_products"}, "edit", "publish", me, true},
		{"edit other draft needs edit_others_", []string{"edit_products"}, "edit", "draft", other, false},
		{"edit other draft granted", []string{"edit_others_products"}, "edit", "draft", other, true},
		{"edit other published needs edit_published_", []string{"edit_others_products"}, "edit", "publish", other, false},
		{"edit other published granted", []string{"edit_others_products", "edit_published_products"}, "edit", "publish", other, true},
		{"edit other private needs edit_private_", []string{"edit_others_products"}, "edit", "private", other, false},
		{"edit other private granted", []string{"edit_others_products", "edit_private_products"}, "edit", "private", other, true},
		{"edit own private no private cap", []string{"edit_products"}, "edit", "private", me, true},
		{"edit post-family ignored", []string{"edit_posts", "edit_others_posts", "edit_published_posts"}, "edit", "publish", other, false},

		// delete
		{"delete own draft", []string{"delete_products"}, "delete", "draft", me, true},
		{"delete own published needs delete_published_", []string{"delete_products"}, "delete", "publish", me, false},
		{"delete own published granted", []string{"delete_products", "delete_published_products"}, "delete", "publish", me, true},
		{"delete other draft needs delete_others_", []string{"delete_products"}, "delete", "draft", other, false},
		{"delete other draft granted", []string{"delete_others_products"}, "delete", "draft", other, true},
		{"delete other published needs delete_published_", []string{"delete_others_products"}, "delete", "publish", other, false},
		{"delete other published granted", []string{"delete_others_products", "delete_published_products"}, "delete", "publish", other, true},
		{"delete other private needs delete_private_", []string{"delete_others_products"}, "delete", "private", other, false},
		{"delete other private granted", []string{"delete_others_products", "delete_private_products"}, "delete", "private", other, true},
		{"delete own private no private cap", []string{"delete_products"}, "delete", "private", me, true},
		{"delete needs delete_ not edit_", []string{"edit_products", "edit_others_products"}, "delete", "draft", other, false},
		{"delete post-family ignored", []string{"delete_posts", "delete_others_posts"}, "delete", "draft", me, false},
	}
	for _, r := range rows {
		a := productsPrincipal(r.caps...)
		var got bool
		switch r.op {
		case "create":
			got = CanCreatePostSuffix(a, suffixProducts, r.status, r.author)
		case "edit":
			got = CanEditPostSuffix(a, suffixProducts, r.status, r.author)
		case "delete":
			got = CanDeletePostSuffix(a, suffixProducts, r.status, r.author)
		default:
			t.Fatalf("unknown op %q", r.op)
		}
		if got != r.want {
			t.Errorf("%s: %s products/%s author=%d: got %v, want %v", r.name, r.op, r.status, r.author, got, r.want)
		}
	}
}

// TestSuffixEquivalentToPostFunctions asserts CanXPost(a, t, ...) ==
// CanXPostSuffix(a, capSuffix(t), ...) for post, page and an unknown type
// ("shop_order") across every role (Req 15.3, 7.4).
func TestSuffixEquivalentToPostFunctions(t *testing.T) {
	const me, other = int64(1), int64(999)
	for _, role := range allRoles {
		a := principalFor(role)
		for _, typ := range []string{"post", "page", "shop_order"} {
			suf := capSuffix(typ)
			for _, status := range []string{"draft", "publish", "private"} {
				for _, author := range []int64{me, other} {
					if g, w := CanCreatePostSuffix(a, suf, status, author), CanCreatePost(a, typ, status, author); g != w {
						t.Errorf("create %s %s/%s author=%d: suffix=%v post=%v", role, typ, status, author, g, w)
					}
					if g, w := CanEditPostSuffix(a, suf, status, author), CanEditPost(a, typ, status, author); g != w {
						t.Errorf("edit %s %s/%s author=%d: suffix=%v post=%v", role, typ, status, author, g, w)
					}
					if g, w := CanDeletePostSuffix(a, suf, status, author), CanDeletePost(a, typ, status, author); g != w {
						t.Errorf("delete %s %s/%s author=%d: suffix=%v post=%v", role, typ, status, author, g, w)
					}
				}
			}
		}
	}
}
