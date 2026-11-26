package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
)

// M19 task 7.1: RED assembly tests for sticky_posts cleanup on post delete
// (Req 8.1-8.5, 8.8, 8.9) and for REST page parent always emitted (Req 6.1,
// 6.2). Every assertion goes through buildHandler as main wires it. The sticky
// tests fail until task 7.2 passes content.WithStickyOptions to
// NewPostWriteService; the page tests fail until task 6.2 drops omitempty.

// stickyMissing is what stickyOption returns when the option row is absent.
const stickyMissing = "<missing>"

// stickySeedOption inserts the sticky_posts option row.
func stickySeedOption(t *testing.T, env customWriteEnv, value string) {
	t.Helper()
	if _, err := env.Repos.DB().ExecContext(context.Background(),
		"INSERT INTO "+env.Prefix+"options (option_name, option_value, autoload) VALUES (?, ?, ?)",
		"sticky_posts", value, "yes"); err != nil {
		t.Fatalf("seed sticky_posts: %v", err)
	}
}

// stickyOption returns the stored sticky_posts value, or stickyMissing.
func stickyOption(t *testing.T, env customWriteEnv) string {
	t.Helper()
	var v string
	err := env.Repos.DB().QueryRowContext(context.Background(),
		"SELECT option_value FROM "+env.Prefix+"options WHERE option_name = ?", "sticky_posts").Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return stickyMissing
	}
	if err != nil {
		t.Fatalf("read sticky_posts: %v", err)
	}
	return v
}

// stickyList renders the PHP serialized form of ids.
func stickyList(ids ...int64) string {
	s := fmt.Sprintf("a:%d:{", len(ids))
	for i, id := range ids {
		s += fmt.Sprintf("i:%d;i:%d;", i, id)
	}
	return s + "}"
}

// stickyWantOption fails unless sticky_posts holds want.
func stickyWantOption(t *testing.T, env customWriteEnv, want, what string) {
	t.Helper()
	if got := stickyOption(t, env); got != want {
		t.Errorf("%s: sticky_posts = %q, want %q", what, got, want)
	}
}

// stickyDelete deletes path as actor and requires status want.
func stickyDelete(t *testing.T, env customWriteEnv, actor *customWriteActor, path string, want int) {
	t.Helper()
	rec := doJSON(t, env.Handler, actor, http.MethodDelete, path, "")
	if rec.Code != want {
		t.Fatalf("DELETE %s: status = %d, want %d; body %s", path, rec.Code, want, rec.Body.String())
	}
}

// stickyFailingWriter is an OptionWriter whose Set always fails.
type stickyFailingWriter struct{ domain.OptionWriter }

func (stickyFailingWriter) Set(context.Context, string, string) error {
	return errors.New("option store unavailable")
}

// TestBuildHandlerSticky: deleting a post removes its id from sticky_posts,
// order kept, through the admin API and REST; pages and custom rows never touch
// the option; a missing or malformed option is left alone.
func TestBuildHandlerSticky(t *testing.T) {
	t.Run("admin then rest delete", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		a := seedRawPost(t, env, "post", "publish", 0)
		b := seedRawPost(t, env, "post", "publish", 0)
		stickySeedOption(t, env, stickyList(a, b))

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/admin/api/posts/%d", a), http.StatusNoContent)
		stickyWantOption(t, env, stickyList(b), "after admin delete of A (Req 8.1)")

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/wp-json/wp/v2/posts/%d?force=true", b), http.StatusOK)
		stickyWantOption(t, env, "a:0:{}", "after REST delete of the last id (Req 8.5)")
	})

	t.Run("non-sticky post leaves option byte-identical", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		a := seedRawPost(t, env, "post", "publish", 0)
		b := seedRawPost(t, env, "post", "publish", 0)
		c := seedRawPost(t, env, "post", "publish", 0)
		stickySeedOption(t, env, stickyList(a, b))

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/admin/api/posts/%d", c), http.StatusNoContent)
		stickyWantOption(t, env, stickyList(a, b), "non-sticky delete (Req 8.2)")
	})

	t.Run("no option row stays absent", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		a := seedRawPost(t, env, "post", "publish", 0)

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/admin/api/posts/%d", a), http.StatusNoContent)
		stickyWantOption(t, env, stickyMissing, "no option (Req 8.3)")
	})

	t.Run("malformed option survives", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		a := seedRawPost(t, env, "post", "publish", 0)
		stickySeedOption(t, env, "not-serialized")

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/admin/api/posts/%d", a), http.StatusNoContent)
		stickyWantOption(t, env, "not-serialized", "malformed option (Req 8.4)")
	})

	t.Run("page and registered row never touch the option", func(t *testing.T) {
		env := newCustomWriteHandler(t, true)
		page := seedRawPost(t, env, "page", "publish", 0)
		product := seedRawPost(t, env, "product", "publish", 0)
		// The ids sit in the list on purpose: a delete of a non-post row must
		// not remove them.
		want := stickyList(page, product)
		stickySeedOption(t, env, want)

		stickyDelete(t, env, &env.Admin, fmt.Sprintf("/admin/api/posts/%d", page), http.StatusNoContent)
		stickyWantOption(t, env, want, "after page delete (Req 8.9)")
		stickyDelete(t, env, &env.Curator, fmt.Sprintf("/admin/api/posts/%d", product), http.StatusNoContent)
		stickyWantOption(t, env, want, "after registered-row delete (Req 8.9)")
	})
}

// TestBuildHandlerStickyWriteFailure (Req 8.8): a failing option writer answers
// 500 and leaves the post, and a retry with the working writer succeeds.
func TestBuildHandlerStickyWriteFailure(t *testing.T) {
	var original domain.OptionWriter
	env := newCustomWriteHandlerWith(t, true, func(repos *storage.Repositories) {
		original = repos.OptionWriter
		repos.OptionWriter = stickyFailingWriter{original}
	})
	a := seedRawPost(t, env, "post", "publish", 0)
	b := seedRawPost(t, env, "post", "publish", 0)
	stickySeedOption(t, env, stickyList(a, b))
	path := fmt.Sprintf("/admin/api/posts/%d?force=true", a)

	stickyDelete(t, env, &env.Admin, path, http.StatusInternalServerError)
	if rowSnapshot(t, env, a) == "<missing>" {
		t.Fatalf("post row removed although the option write failed")
	}
	stickyWantOption(t, env, stickyList(a, b), "after failed delete")

	env.Repos.OptionWriter = original
	h, _ := assemblyHandler(t, env.Repos, config.DatabaseConfig{Vendor: "sqlite", TablePrefix: env.Prefix}, customWriteRegistry(t))
	rec := doJSON(t, h, &env.Admin, http.MethodDelete, path, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("retry DELETE: status = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if rowSnapshot(t, env, a) != "<missing>" {
		t.Errorf("post row still exists after retry")
	}
	stickyWantOption(t, env, stickyList(b), "after retry")
}

// TestBuildHandlerStickyRESTPageParent (Req 6.1, 6.2): REST pages always carry
// the key parent, 0 for a top-level page and the parent id for a child.
func TestBuildHandlerStickyRESTPageParent(t *testing.T) {
	env := newCustomWriteHandler(t, true)
	create := func(slug string, parent int64) int64 {
		t.Helper()
		body := fmt.Sprintf(`{"title":%q,"slug":%q,"content":"x","status":"publish","parent":%d}`, slug, slug, parent)
		rec := doJSON(t, env.Handler, &env.Admin, http.MethodPost, "/wp-json/wp/v2/pages", body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create page %s: status = %d; body %s", slug, rec.Code, rec.Body.String())
		}
		var out struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.ID == 0 {
			t.Fatalf("create page %s: decode %q: %v", slug, rec.Body.String(), err)
		}
		return out.ID
	}
	top := create("sticky-top", 0)
	child := create("sticky-child", top)

	for _, c := range []struct {
		name string
		id   int64
		want float64
	}{{"top-level", top, 0}, {"child", child, float64(top)}} {
		rec := doJSON(t, env.Handler, nil, http.MethodGet, fmt.Sprintf("/wp-json/wp/v2/pages/%d", c.id), "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s page: status = %d; body %s", c.name, rec.Code, rec.Body.String())
		}
		var m map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
			t.Fatalf("GET %s page: decode: %v", c.name, err)
		}
		got, ok := m["parent"]
		if !ok {
			t.Errorf("%s page: key parent absent, want %v; body %s", c.name, c.want, rec.Body.String())
			continue
		}
		if got != c.want {
			t.Errorf("%s page: parent = %v, want %v", c.name, got, c.want)
		}
	}
}
