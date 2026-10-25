package web_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/roboweaver/grimoire/internal/admin"
	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/render"
	"github.com/roboweaver/grimoire/internal/sanitize"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/storagetest"
	"github.com/roboweaver/grimoire/internal/web"
)

// This file proves M13 Requirement 7 / 9.4: after a child category is
// re-parented through the now-wired term write path, M9b's descendant-aware
// archive counts (the EXISTS semi-join in PostRepo.archiveWhere, read live from
// term_taxonomy.parent on every request) recompute with no write-side
// bookkeeping. It adds NO production code -- phases 1-4 made the re-parent write
// term_taxonomy.parent, and the read path has always read it live; this test is
// the evidence that the two compose correctly.
//
// It layers storagetest.SeedArchiveFixtures (the same seed the storage archive
// contract uses, RunArchiveContract) over the live wp-json term write router, so
// the re-parent travels the real PUT /wp-json/wp/v2/categories/{id} chain into
// TermRepo.Update, and the archive totals are read back through the real
// content.TermService.CategoryArchive (Archive.Page.Total / the listing), not a
// reimplementation.
//
// Fixture shape (SeedFixtures + SeedNestedCategories + SeedArchiveExtras):
//
//	categories (term id, parent)              published posts in the subtree
//	  news      10, 0                         hello-2, hello-3
//	  tech      50, 0                         tech-post, dual-post
//	   └─ go    51, 50                         go-post, dual-post (dual filed under BOTH tech and go)
//	       └─ generics 52, 51                  generics-post
//
// dual-post (404) is filed under BOTH tech (50) and go (51): it is the
// appears-once probe. While go is beneath tech, tech's descendant set is
// {50,51,52} and dual-post matches the term predicate twice (via tech directly
// and via go), yet the EXISTS semi-join lists and counts it exactly once.

// newReparentArchiveServer builds the same live wp-json term write + archive
// server newRESTTermsWriteRouter does, but seeds SeedArchiveFixtures so there is
// a parent->child chain with posts (and a dual-filed post) to re-parent. It
// returns the handler (for the REST write), the seeded repositories (so the test
// can read archives through a real content.TermService over the same database),
// and the ApplicationPasswords manager (to mint the manage_categories principal).
func newReparentArchiveServer(t *testing.T) (http.Handler, *storage.Repositories, *auth.ApplicationPasswords) {
	t.Helper()
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "grimoire.db")
	cfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(cfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { repos.Close() })

	migFS, err := storage.MigrationsFS(cfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := storagetest.SeedFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedFixtures: %v", err)
	}
	// The parent->child chain, the per-level posts, and the dual-filed post.
	if err := storagetest.SeedArchiveFixtures(ctx, repos.DB(), cfg.Vendor, cfg.TablePrefix); err != nil {
		t.Fatalf("SeedArchiveFixtures: %v", err)
	}

	caps, err := auth.SerializeCapabilities(auth.RoleAdministrator)
	if err != nil {
		t.Fatalf("SerializeCapabilities: %v", err)
	}
	if err := repos.UserMeta.Set(ctx, 1, cfg.TablePrefix+"capabilities", caps); err != nil {
		t.Fatalf("seed admin capabilities: %v", err)
	}

	eng, err := render.Load(filepath.Join("..", "..", "themes"), "default")
	if err != nil {
		t.Fatalf("render.Load: %v", err)
	}
	adminSvc := content.NewAdminService(
		repos.AdminPosts, repos.PostWriter, repos.PostCounter,
		repos.UserCounter, repos.TermCounter, repos.Users,
	)
	mapper := content.NewRESTMapper(repos.PostTerms, repos.PostMeta, repos.UserMeta, cfg.TablePrefix)
	comments := content.NewCommentService(repos.Comments, repos.CommentWriter, repos.CommentMeta, repos.PostWriter, content.NewBasicCommentSpamFilter(content.BasicCommentSpamFilterConfig{}), sanitize.New())
	ap := &auth.ApplicationPasswords{Users: repos.Users, Meta: repos.UserMeta, Prefix: cfg.TablePrefix}
	termWrite := content.NewTermWriteService(termReadWriter{TermWriter: repos.TermWriter, TermReader: repos.TermReader})
	srv := web.NewServer(
		content.NewPostService(repos.Posts).WithAuthors(repos.Users),
		content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader),
		content.NewOptionService(repos.Options),
		eng,
		nil,
	).WithAuth(&fakeSessions{}, web.AuthConfig{}).
		WithAdmin(admin.Handler("/admin"), adminSvc).
		WithAdminWrites(nil, termWrite, nil, nil).
		WithContentFeatures(comments, nil, nil).
		WithREST(mapper, repos.AdminPosts, repos.PostWriter, repos.Posts, repos.Media, repos.Users, 0).
		WithApplicationPasswords(ap, false, "")
	return srv.Routes(), repos, ap
}

// categoryArchive reads one category archive through the real
// content.TermService.CategoryArchive over the given repositories -- the same
// descendant-aware read path the site serves, which loads TermHierarchy from
// term_taxonomy.parent live and counts over the EXISTS semi-join. It returns the
// resolved Archive so a caller can assert on Page.Total and the listing.
func categoryArchive(t *testing.T, repos *storage.Repositories, segments ...string) content.Archive {
	t.Helper()
	svc := content.NewTermService(repos.Terms, repos.Posts).WithHierarchy(repos.TermReader)
	archive, err := svc.CategoryArchive(context.Background(), segments, 1, content.DefaultPerPage)
	if err != nil {
		t.Fatalf("CategoryArchive(%v): %v", segments, err)
	}
	return archive
}

// countSlug reports how many times a given post slug appears in an archive's
// listing. The appears-once guarantee means a post filed under both a parent and
// a descendant in the same archive must come back with a count of exactly 1.
func countSlug(posts []domain.Post, slug string) int {
	n := 0
	for _, p := range posts {
		if p.Slug == slug {
			n++
		}
	}
	return n
}

// TestReparentRecomputesDescendantArchiveCounts is the M13 count-after-reparent
// integration test (Req 6.3, 7.1-7.4, 9.4). It asserts that moving the "go"
// category out from under "tech" and under "news" is reflected on the next
// archive read with no write-side bookkeeping: tech's total drops the moved
// subtree's posts, news's total gains them, and the dual-filed post still
// appears exactly once in each archive (the EXISTS semi-join's appears-once
// guarantee holding across a re-parent).
func TestReparentRecomputesDescendantArchiveCounts(t *testing.T) {
	h, repos, ap := newReparentArchiveServer(t)
	secret := mintAppPassword(t, context.Background(), ap)

	const (
		techTotalBefore = 4 // tech-post, go-post, generics-post, dual-post (once)
		newsTotalBefore = 2 // hello-2, hello-3
		techTotalAfter  = 2 // tech-post, dual-post (go subtree gone; dual still under tech directly)
		newsTotalAfter  = 5 // hello-2, hello-3, go-post, generics-post, dual-post (once)
	)

	// --- Before the re-parent: tech carries its whole subtree. ---
	tech := categoryArchive(t, repos, "tech")
	if tech.Page.Total != techTotalBefore {
		t.Fatalf("before: tech archive Total = %d, want %d (tech + go + generics subtree)",
			tech.Page.Total, techTotalBefore)
	}
	if tech.Page.Total != len(tech.Posts) {
		t.Fatalf("before: tech Total %d disagrees with listing len %d -- count and listing must describe the same set",
			tech.Page.Total, len(tech.Posts))
	}
	// dual-post is filed under both tech (directly) and go (a descendant): it
	// matches the term predicate twice but must appear exactly once.
	if got := countSlug(tech.Posts, storagetest.ArchiveDualPostSlug); got != 1 {
		t.Fatalf("before: dual-post appears %d times in tech archive, want exactly 1 (appears-once guarantee)", got)
	}

	news := categoryArchive(t, repos, "news")
	if news.Page.Total != newsTotalBefore {
		t.Fatalf("before: news archive Total = %d, want %d (hello-2, hello-3)", news.Page.Total, newsTotalBefore)
	}

	// --- Re-parent "go" (term 51) from "tech" (50) to "news" (10) through the
	// real wp-json term write chain: PUT /wp-json/wp/v2/categories/51. ---
	rec := doRESTWrite(t, h, http.MethodPut, "/wp-json/wp/v2/categories/51", secret, `{"parent":10}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("re-parent go under news: status = %d, body = %s", rec.Code, rec.Body.String())
	}
	// The write landed in term_taxonomy.parent: the REST read-back confirms it.
	got := restTermParentGet(t, h, "/wp-json/wp/v2/categories/51")
	if got["parent"].(float64) != 10 {
		t.Fatalf("after re-parent, go's parent = %v, want 10", got["parent"])
	}

	// --- After the re-parent: the descendant sets are recomputed live. ---
	tech = categoryArchive(t, repos, "tech")
	if tech.Page.Total != techTotalAfter {
		t.Fatalf("after: tech archive Total = %d, want %d (go subtree re-parented away; dual-post stays via its direct tech filing)",
			tech.Page.Total, techTotalAfter)
	}
	if tech.Page.Total != len(tech.Posts) {
		t.Fatalf("after: tech Total %d disagrees with listing len %d", tech.Page.Total, len(tech.Posts))
	}
	// The dual-filed post is still under tech directly, so it still appears --
	// exactly once.
	if got := countSlug(tech.Posts, storagetest.ArchiveDualPostSlug); got != 1 {
		t.Fatalf("after: dual-post appears %d times in tech archive, want exactly 1", got)
	}

	// news (term 10) is now go's parent: its descendant set is {news, go,
	// generics} and its archive gains the moved subtree's posts.
	news = categoryArchive(t, repos, "news")
	if news.Page.Total != newsTotalAfter {
		t.Fatalf("after: news archive Total = %d, want %d (hello-2, hello-3 + go-post, generics-post, dual-post)",
			news.Page.Total, newsTotalAfter)
	}
	if news.Page.Total != len(news.Posts) {
		t.Fatalf("after: news Total %d disagrees with listing len %d", news.Page.Total, len(news.Posts))
	}
	// dual-post is now filed under go, which is a descendant of news: it must
	// still appear exactly once in the news archive (no double-count).
	if got := countSlug(news.Posts, storagetest.ArchiveDualPostSlug); got != 1 {
		t.Fatalf("after: dual-post appears %d times in news archive, want exactly 1 (appears-once across the re-parent)", got)
	}

	// A re-parent must move posts, not duplicate them: a post that was in tech's
	// archive and is now in news's must have left tech's. Assert the moved
	// post's net migration via the go-post slug, which is filed only under go.
	if got := countSlug(tech.Posts, storagetest.NestedChildPostSlug); got != 0 {
		t.Fatalf("after: go-post still appears %d times under tech, want 0 (it moved to news)", got)
	}
	if got := countSlug(news.Posts, storagetest.NestedChildPostSlug); got != 1 {
		t.Fatalf("after: go-post appears %d times under news, want exactly 1", got)
	}
}
