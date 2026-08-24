// Package extensions implements grimoire's native Go extension mechanism: a
// compile-time hook registry providing WordPress-flavored actions
// (fire-and-forget notifications, do_action) and filters
// (value-transforming pipelines, apply_filters), typed via Go generics.
//
// This is explicitly NOT a WordPress-plugin-compatible runtime. grimoire does
// not run PHP, does not load .php files from a wp-content/plugins/ directory
// (or any directory), and provides no plugin marketplace, no plugin
// activation/deactivation UI, and no plugin-update mechanism. An extension is
// Go source code compiled directly into the grimoire binary at build time —
// there is no supported mechanism for installing an extension into a running
// grimoire instance without a rebuild, and no dynamic loading of any kind
// (no .so/.dll plugin files, no separate process, no interpreter).
//
// What this package *does* provide: a small, defined set of Go hook points
// (see the callers of DoAction/ApplyFilters throughout grimoire, e.g. the
// post-render filter, the REST request/response hooks, and the
// comment-submit action) that a first-party, vendored, or external Go
// package can register callbacks against using the API below, sufficient to
// observe or transform grimoire's behavior at those points without modifying
// grimoire's core code.
//
// Unlike every other package in this repository, pkg/extensions is
// deliberately NOT under internal/ — it is grimoire's first pkg/ directory.
// Go's own import-visibility rule means a package under internal/ can only
// be imported by code rooted at the same module (github.com/roboweaver/
// grimoire); an external Go module authoring a grimoire extension needs to
// import this registration API from outside that module tree, which
// requires it to live at an importable, non-internal path.
//
// A typical extension registers its callbacks from a package-level init(),
// and is linked into the grimoire binary via a blank import (commonly from
// cmd/grimoire/main.go):
//
//	import _ "github.com/example/my-grimoire-extension"
//
// Registration is expected only at init() time, before any request is
// served; firing (DoAction/ApplyFilters) happens on every request, from many
// goroutines, and is safe for concurrent use.
package extensions
