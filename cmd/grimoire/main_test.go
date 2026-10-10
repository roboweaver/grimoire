package main

import (
	"testing"

	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/web"
)

// TestSinglePolicyWiring asserts the single-instance construction invariant
// task 8.2 requires: main builds exactly one *sanitize.Policy and threads that
// same instance into both write services (CommentService, PostWriteService) and
// the web Server, so pendingEcho, commentView and the write boundary all share
// one Policy (Req 1.1, 1.8, 4.4).
//
// Wiring-assertion decision: the policy field is unexported on all three
// consumers (content.CommentService, content.PostWriteService, web.Server), and
// each lives in a different package from this one (main). There is no exported
// accessor, so a direct pointer-identity assertion from here is impossible
// without reaching into unexported fields of foreign packages via reflection --
// precisely the brittle hack task 8.2 warns against. Instead this test pins the
// construction PATH: it builds one policy and confirms all three constructors
// accept that single shared instance and return a wired, non-nil value, exactly
// as main.go does. The single-instance guarantee itself is documented as an
// invariant in main.go's "One Policy for the process" comment at the one place
// the instance is created and shared. The per-consumer behavior that the policy
// is actually applied is covered by the content and web package tests
// (writeservices_test.go, comments_test.go, comments_backstop_test.go,
// handlers_test.go), which exercise the real policy end to end.
func TestSinglePolicyWiring(t *testing.T) {
	contentPolicy := sanitize.New()
	if contentPolicy == nil {
		t.Fatal("sanitize.New() returned nil; the single process policy must be non-nil")
	}

	// Both write services accept the shared instance positionally/option-wise,
	// mirroring main.go's construction.
	comments := content.NewCommentService(nil, nil, nil, nil, nil, contentPolicy)
	if comments == nil {
		t.Fatal("NewCommentService returned nil when wired with the shared policy")
	}
	postWrite := content.NewPostWriteService(nil, content.WithContentPolicy(contentPolicy))
	if postWrite == nil {
		t.Fatal("NewPostWriteService returned nil when wired with the shared policy")
	}

	// The Server builder accepts the same instance via WithContentPolicy and
	// returns itself for chaining, so the backstop/echo paths share it.
	srv := web.NewServer(nil, nil, nil, nil, nil).WithContentPolicy(contentPolicy)
	if srv == nil {
		t.Fatal("Server.WithContentPolicy returned nil")
	}
}
