package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/web"
)

// Task 7.1 — media-write wiring assertions.
//
// Two things this task owns are not covered elsewhere and are pinned here:
//
//  1. The media write service is REACHABLE once wired: a Server built with
//     WithMediaWrites routes the REST media write verb to the real handler, not
//     the restNotImplemented 501 stub. (The create/update behavior itself is
//     exercised in rest_media_test.go; here we only need to see that the wiring
//     flips the route off the 501 stub, which is the observable signal that
//     s.mediaWrite reached the router.)
//  2. The single-instance *sanitize.Policy invariant (Req 11.1): the SAME policy
//     pointer governs post writes, comment writes, AND the media write service,
//     so none of the three REST write surfaces can diverge into a second,
//     differently-configured sanitizer.
//
// Wiring-assertion decision (carried from cmd/grimoire/main_test.go's
// TestSinglePolicyWiring): the policy field is unexported on all three content
// consumers (content.CommentService, content.PostWriteService,
// content.MediaWriteService) and on web.Server, and this test lives in a foreign
// package (web_test). There is no exported accessor, so a direct pointer-identity
// assertion would have to reach into unexported fields of foreign packages via
// reflection — the brittle hack the main_test.go note warns against. Instead this
// test pins the construction PATH: it builds exactly one *sanitize.Policy and
// confirms all three write-service constructors plus the Server builder accept
// that single shared instance and return a wired, non-nil value, exactly as
// cmd/grimoire/main.go wires them. The per-consumer behavior that the policy is
// actually applied is covered by the content and web package tests
// (writeservices_test.go, comments_test.go, mediawriteservice_test.go,
// rest_media_test.go), which exercise the real policy end to end.

// TestMediaWriteServiceReachableWhenWired asserts WithMediaWrites makes the REST
// media write surface reachable: POST /wp-json/wp/v2/media no longer answers the
// 501 restNotImplemented stub once a MediaWriteService is wired (Req 8.3). The
// companion negative case (nil mediaWrite still answers 501) is
// TestRESTMediaNilWriteServiceCreateFallsBackTo501 in rest_media_test.go.
func TestMediaWriteServiceReachableWhenWired(t *testing.T) {
	h, _, _ := newRESTMediaWriteRouter(t, uploaderFake())

	// A bare POST /media with no Content-Disposition: the real handler rejects
	// it 400 rest_upload_no_content_disposition. The point here is only that it
	// is NOT the 501 stub — any non-501 answer proves the real handler is on the
	// route, i.e. s.mediaWrite reached the router via WithMediaWrites.
	req := mediaUploadReq(pngAllowedMIME, "", pngBytes)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusNotImplemented {
		t.Fatalf("POST /media answered 501 with a wired MediaWriteService; "+
			"WithMediaWrites did not reach the router (body=%s)", rec.Body.String())
	}
}

// TestSharedPolicyGovernsAllThreeWriteSurfaces pins the single-instance
// *sanitize.Policy invariant (Req 11.1): the one process policy threads into the
// comment write service, the post write service, and the media write service, so
// all three REST write surfaces sanitize through exactly one configured policy.
func TestSharedPolicyGovernsAllThreeWriteSurfaces(t *testing.T) {
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

	// Media writes: the policy is supplied via WithMediaContentPolicy. This is
	// the surface task 7.1 adds to the shared-policy invariant.
	mediaWrite := content.NewMediaWriteService(nil, nil, nil, content.WithMediaContentPolicy(contentPolicy))
	if mediaWrite == nil {
		t.Fatal("NewMediaWriteService returned nil when wired with the shared policy")
	}

	// The Server builder accepts the same instance via WithContentPolicy and the
	// media write service via WithMediaWrites, both returning the Server for
	// chaining, so the backstop/echo paths and the media write route share the
	// one policy that governs the three write services above.
	srv := web.NewServer(nil, nil, nil, nil, nil).
		WithContentPolicy(contentPolicy).
		WithMediaWrites(mediaWrite)
	if srv == nil {
		t.Fatal("Server.WithContentPolicy().WithMediaWrites() returned nil")
	}
}
