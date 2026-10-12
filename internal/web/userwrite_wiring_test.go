package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/web"
)

// Task 6.1 — user-write wiring assertions, the twin of mediawrite_wiring_test.go.
//
// Two things this task owns are not covered elsewhere and are pinned here:
//
//  1. The user write service is REACHABLE once wired: a Server built with
//     WithUserWrites routes the REST user write verb to the real handler, not
//     the restNotImplemented 501 stub. (The create/update behavior itself is
//     exercised in rest_users_test.go; here we only need to see that the wiring
//     flips the route off the 501 stub, which is the observable signal that
//     s.userWrite reached the router.)
//  2. The single-instance *sanitize.Policy invariant (Req 11.1, 4.2): the SAME
//     policy pointer governs post writes, comment writes, media writes, AND the
//     user write service, so none of the four REST write surfaces can diverge
//     into a second, differently-configured sanitizer.
//
// Wiring-assertion decision (carried from mediawrite_wiring_test.go): the policy
// field is unexported on every content consumer (content.CommentService,
// content.PostWriteService, content.MediaWriteService, content.UserService) and
// on web.Server, and this test lives in a foreign package (web_test). There is
// no exported accessor, so a direct pointer-identity assertion would have to
// reach into unexported fields of foreign packages via reflection — the brittle
// hack that note warns against. Instead this test pins the construction PATH: it
// builds exactly one *sanitize.Policy and confirms all four write-service
// constructors plus the Server builder accept that single shared instance and
// return a wired, non-nil value, exactly as cmd/grimoire/main.go wires them. The
// per-consumer behavior that the policy is actually applied is covered by the
// content and web package tests (writeservices_test.go, comments_test.go,
// mediawriteservice_test.go, userservice_test.go, rest_users_test.go), which
// exercise the real policy end to end.

// TestUserWriteServiceReachableWhenWired asserts WithUserWrites makes the REST
// user write surface reachable: POST /wp-json/wp/v2/users no longer answers the
// 501 restNotImplemented stub once a UserService is wired (Req 9.3). The
// companion negative case (nil userWrite still answers 501 for every single-item
// write verb) is TestRESTUserWriteVerbsNilServiceFallBackTo501 in
// rest_users_test.go.
func TestUserWriteServiceReachableWhenWired(t *testing.T) {
	h, _ := newRESTUserWriteRouter(t, adminUserFake())

	// A POST /users whose body omits the required fields: the real handler
	// rejects it 400 rest_missing_callback_param. The point here is only that it
	// is NOT the 501 stub — any non-501 answer proves the real handler is on the
	// route, i.e. s.userWrite reached the router via WithUserWrites.
	req := userCreateReq(`{}`)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("POST /users answered 501 with a wired UserService; "+
			"WithUserWrites did not reach the router (body=%s)", rec.Body.String())
	}
}

// TestSharedPolicyGovernsAllFourWriteSurfaces pins the single-instance
// *sanitize.Policy invariant (Req 11.1, 4.2): the one process policy threads into
// the comment write service, the post write service, the media write service,
// AND the user write service, so all four REST write surfaces sanitize through
// exactly one configured policy.
func TestSharedPolicyGovernsAllFourWriteSurfaces(t *testing.T) {
	// One policy for the process, as cmd/grimoire/main.go builds it.
	contentPolicy := sanitize.New()
	if contentPolicy == nil {
		t.Fatal("sanitize.New() returned nil; the single process policy must be non-nil")
	}

	// Comment writes: the policy is a required positional argument.
	comments := content.NewCommentService(nil, nil, nil, nil, nil, contentPolicy)
	if comments == nil {
		t.Fatal("NewCommentService returned nil when wired with the shared policy")
	}

	// Post writes: the policy is supplied via WithContentPolicy.
	postWrite := content.NewPostWriteService(nil, content.WithContentPolicy(contentPolicy))
	if postWrite == nil {
		t.Fatal("NewPostWriteService returned nil when wired with the shared policy")
	}

	// Media writes: the policy is supplied via WithMediaContentPolicy.
	mediaWrite := content.NewMediaWriteService(nil, nil, nil, content.WithMediaContentPolicy(contentPolicy))
	if mediaWrite == nil {
		t.Fatal("NewMediaWriteService returned nil when wired with the shared policy")
	}

	// User writes: the policy is supplied via WithUserContentPolicy. This is the
	// surface task 6.1 adds to the shared-policy invariant.
	userWrite := content.NewUserService(nil, nil, "wp_", content.WithUserContentPolicy(contentPolicy))
	if userWrite == nil {
		t.Fatal("NewUserService returned nil when wired with the shared policy")
	}

	// The Server builder accepts the same instance via WithContentPolicy, the
	// media write service via WithMediaWrites, and the user write service via
	// WithUserWrites, all returning the Server for chaining, so the backstop/echo
	// paths and the media + user write routes share the one policy that governs
	// the four write services above.
	srv := web.NewServer(nil, nil, nil, nil, nil).
		WithContentPolicy(contentPolicy).
		WithMediaWrites(mediaWrite).
		WithUserWrites(userWrite)
	if srv == nil {
		t.Fatal("Server.WithContentPolicy().WithMediaWrites().WithUserWrites() returned nil")
	}
}
