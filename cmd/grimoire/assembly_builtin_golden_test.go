package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// updateBuiltinGolden regenerates cmd/grimoire/testdata/builtin_golden/*.json.
// The files were generated from the unmodified (pre-M17) tree; they describe
// built-in post, page, category and post_tag behavior, and must keep passing
// through every later M17 phase (Requirement 15).
var updateBuiltinGolden = flag.Bool("update", false, "rewrite the builtin golden files")

// goldenStep is one recorded response.
type goldenStep struct {
	Name    string            `json:"name"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    any               `json:"body,omitempty"`
}

// volatileKeys are values that change from run to run. Only the value is
// replaced; the key stays so a removed or renamed key still shows as a diff.
var volatileKeys = map[string]bool{
	"date": true, "date_gmt": true, "modified": true, "modified_gmt": true,
	"createdAt": true, "updatedAt": true, "publishedAt": true, "modifiedAt": true,
	"csrfToken": true,
}

var tsRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})?`)

// normalizeGolden replaces volatile values in a decoded JSON document.
func normalizeGolden(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if volatileKeys[k] {
				if _, isStr := val.(string); isStr {
					t[k] = "<ts>"
					continue
				}
			}
			t[k] = normalizeGolden(val)
		}
		return t
	case []any:
		for i := range t {
			t[i] = normalizeGolden(t[i])
		}
		return t
	case string:
		return tsRE.ReplaceAllString(t, "<ts>")
	}
	return v
}

// goldenVars holds ids captured from earlier responses, substituted into later
// paths as {name}.
type goldenVars map[string]int64

func (v goldenVars) sub(s string) string {
	for k, id := range v {
		s = strings.ReplaceAll(s, "{"+k+"}", fmt.Sprint(id))
	}
	return s
}

// goldenReq is one scripted request.
type goldenReq struct {
	name, actor, method, path, body string
	save                            string // capture response "id" under this name
}

// builtinGoldenScript drives built-in posts, pages, categories and tags over
// REST and the admin API. It is the same script for both registry settings.
func builtinGoldenScript() []goldenReq {
	return []goldenReq{
		// REST posts.
		{name: "rest post create", actor: "editor", method: "POST", path: "/wp-json/wp/v2/posts",
			body: `{"title":"Golden Post","content":"<p>body</p><script>x()</script>","excerpt":"ex","status":"publish"}`, save: "rpost"},
		{name: "rest post get", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts/{rpost}"},
		{name: "rest post patch", actor: "editor", method: "PATCH", path: "/wp-json/wp/v2/posts/{rpost}", body: `{"title":"Golden Post 2","slug":"golden-two"}`},
		{name: "rest post put", actor: "editor", method: "PUT", path: "/wp-json/wp/v2/posts/{rpost}", body: `{"excerpt":"ex2"}`},
		{name: "rest post list", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts"},
		{name: "rest post contributor forbidden", actor: "contrib", method: "PATCH", path: "/wp-json/wp/v2/posts/{rpost}", body: `{"title":"nope"}`},
		{name: "rest post missing title", actor: "editor", method: "POST", path: "/wp-json/wp/v2/posts", body: `{"status":"publish"}`},
		{name: "rest post delete", actor: "editor", method: "DELETE", path: "/wp-json/wp/v2/posts/{rpost}"},
		{name: "rest post get deleted", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts/{rpost}"},
		// REST pages.
		{name: "rest page create", actor: "editor", method: "POST", path: "/wp-json/wp/v2/pages",
			body: `{"title":"Golden Page","content":"<p>page</p>","status":"publish"}`, save: "rpage"},
		{name: "rest page get", actor: "editor", method: "GET", path: "/wp-json/wp/v2/pages/{rpage}"},
		{name: "rest page patch", actor: "editor", method: "PATCH", path: "/wp-json/wp/v2/pages/{rpage}", body: `{"title":"Golden Page 2"}`},
		{name: "rest page list", actor: "editor", method: "GET", path: "/wp-json/wp/v2/pages"},
		{name: "rest page delete", actor: "editor", method: "DELETE", path: "/wp-json/wp/v2/pages/{rpage}"},
		// REST categories (with parent) and tags.
		{name: "rest category create", actor: "admin", method: "POST", path: "/wp-json/wp/v2/categories", body: `{"name":"Golden Cat"}`, save: "rcat"},
		{name: "rest category child create", actor: "admin", method: "POST", path: "/wp-json/wp/v2/categories",
			body: `{"name":"Golden Child","parent":{rcat}}`, save: "rchild"},
		{name: "rest category get", actor: "admin", method: "GET", path: "/wp-json/wp/v2/categories/{rchild}"},
		{name: "rest category patch", actor: "admin", method: "PATCH", path: "/wp-json/wp/v2/categories/{rchild}", body: `{"name":"Golden Child 2","parent":0}`},
		{name: "rest category cycle", actor: "admin", method: "PATCH", path: "/wp-json/wp/v2/categories/{rcat}", body: `{"parent":{rchild}}`},
		{name: "rest category list", actor: "admin", method: "GET", path: "/wp-json/wp/v2/categories"},
		{name: "rest category forbidden", actor: "contrib", method: "POST", path: "/wp-json/wp/v2/categories", body: `{"name":"x"}`},
		{name: "rest category delete", actor: "admin", method: "DELETE", path: "/wp-json/wp/v2/categories/{rchild}"},
		{name: "rest tag create", actor: "admin", method: "POST", path: "/wp-json/wp/v2/tags", body: `{"name":"Golden Tag"}`, save: "rtag"},
		{name: "rest tag parent rejected", actor: "admin", method: "POST", path: "/wp-json/wp/v2/tags", body: `{"name":"Bad Tag","parent":{rcat}}`},
		{name: "rest tag get", actor: "admin", method: "GET", path: "/wp-json/wp/v2/tags/{rtag}"},
		{name: "rest tag patch", actor: "admin", method: "PATCH", path: "/wp-json/wp/v2/tags/{rtag}", body: `{"name":"Golden Tag 2"}`},
		{name: "rest tag list", actor: "admin", method: "GET", path: "/wp-json/wp/v2/tags"},
		{name: "rest tag delete", actor: "admin", method: "DELETE", path: "/wp-json/wp/v2/tags/{rtag}"},
		// Admin API posts and pages.
		{name: "admin post create", actor: "editor", method: "POST", path: "/admin/api/posts",
			body: `{"title":"Admin Post","content":"<p>a</p>","status":"publish","type":"post"}`, save: "apost"},
		{name: "admin post get", actor: "editor", method: "GET", path: "/admin/api/posts/{apost}"},
		{name: "admin post update", actor: "editor", method: "PUT", path: "/admin/api/posts/{apost}",
			body: `{"title":"Admin Post 2","content":"<p>a2</p>","status":"publish","type":"post","modified":"{modified}"}`},
		{name: "admin post list", actor: "editor", method: "GET", path: "/admin/api/posts?type=post"},
		{name: "admin post bad type", actor: "editor", method: "POST", path: "/admin/api/posts", body: `{"title":"x","status":"draft","type":"nonsense"}`},
		{name: "admin post contributor publish", actor: "contrib", method: "POST", path: "/admin/api/posts", body: `{"title":"c","status":"publish","type":"post"}`},
		{name: "admin post delete", actor: "editor", method: "DELETE", path: "/admin/api/posts/{apost}"},
		{name: "admin page create", actor: "editor", method: "POST", path: "/admin/api/posts",
			body: `{"title":"Admin Page","content":"<p>p</p>","status":"publish","type":"page"}`, save: "apage"},
		{name: "admin page get", actor: "editor", method: "GET", path: "/admin/api/posts/{apage}"},
		{name: "admin page update", actor: "editor", method: "PUT", path: "/admin/api/posts/{apage}",
			body: `{"title":"Admin Page 2","content":"<p>p2</p>","status":"publish","type":"page","modified":"{modified}"}`},
		{name: "admin page list", actor: "editor", method: "GET", path: "/admin/api/posts?type=page"},
		{name: "admin page delete", actor: "editor", method: "DELETE", path: "/admin/api/posts/{apage}"},
		// Admin API terms.
		{name: "admin term create category", actor: "admin", method: "POST", path: "/admin/api/terms",
			body: `{"name":"Admin Cat","slug":"admin-cat","taxonomy":"category"}`, save: "acat"},
		{name: "admin term create child", actor: "admin", method: "POST", path: "/admin/api/terms",
			body: `{"name":"Admin Child","slug":"admin-child","taxonomy":"category","parent":{acat}}`, save: "achild"},
		{name: "admin term create tag", actor: "admin", method: "POST", path: "/admin/api/terms",
			body: `{"name":"Admin Tag","slug":"admin-tag","taxonomy":"post_tag"}`, save: "atag"},
		{name: "admin term update", actor: "admin", method: "PUT", path: "/admin/api/terms/{achild}", body: `{"name":"Admin Child 2","slug":"admin-child-2"}`},
		{name: "admin terms list", actor: "admin", method: "GET", path: "/admin/api/terms"},
		{name: "admin terms list category", actor: "admin", method: "GET", path: "/admin/api/terms?taxonomy=category"},
		{name: "admin terms list unknown", actor: "admin", method: "GET", path: "/admin/api/terms?taxonomy=nonsense"},
		{name: "admin term forbidden", actor: "contrib", method: "POST", path: "/admin/api/terms", body: `{"name":"x","slug":"x","taxonomy":"category"}`},
		{name: "admin term delete", actor: "admin", method: "DELETE", path: "/admin/api/terms/{achild}"},
		// M22 PR 1: REST trash and restore. An empty actor is anonymous.
		{name: "rest trash create", actor: "editor", method: "POST", path: "/wp-json/wp/v2/posts",
			body: `{"title":"Golden Trash","content":"<p>t</p>","status":"publish"}`, save: "rtrash"},
		{name: "rest trash delete", actor: "editor", method: "DELETE", path: "/wp-json/wp/v2/posts/{rtrash}"},
		{name: "rest trash delete again", actor: "editor", method: "DELETE", path: "/wp-json/wp/v2/posts/{rtrash}"},
		{name: "rest trash list default", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts"},
		{name: "rest trash list status trash", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts?status=trash"},
		{name: "rest trash list anonymous", actor: "", method: "GET", path: "/wp-json/wp/v2/posts?status=trash"},
		{name: "rest trash restore", actor: "editor", method: "PATCH", path: "/wp-json/wp/v2/posts/{rtrash}", body: `{"status":"draft"}`},
		{name: "rest trash force delete", actor: "editor", method: "DELETE", path: "/wp-json/wp/v2/posts/{rtrash}?force=true"},
		{name: "rest trash gone", actor: "editor", method: "GET", path: "/wp-json/wp/v2/posts/{rtrash}"},
	}
}

// runBuiltinGolden runs the script against a fresh handler and returns the
// normalized recording.
func runBuiltinGolden(t *testing.T, withRegistry bool) []goldenStep {
	t.Helper()
	env := newCustomWriteHandler(t, withRegistry)
	actors := map[string]*customWriteActor{
		"admin": &env.Admin, "editor": &env.Editor, "author": &env.Author, "contrib": &env.Contrib,
	}
	vars := goldenVars{}
	modified := ""
	var steps []goldenStep
	for _, rq := range builtinGoldenScript() {
		rec := doJSON(t, env.Handler, actors[rq.actor], rq.method, vars.sub(rq.path), strings.ReplaceAll(vars.sub(rq.body), "{modified}", modified))
		st := goldenStep{Name: rq.name, Status: rec.Code, Headers: map[string]string{}}
		for _, h := range []string{"Content-Type", "Location", "Allow", "X-WP-Total", "X-WP-TotalPages"} {
			if v := rec.Header().Get(h); v != "" {
				st.Headers[h] = v
			}
		}
		if raw := bytes.TrimSpace(rec.Body.Bytes()); len(raw) > 0 {
			var doc any
			if err := json.Unmarshal(raw, &doc); err != nil {
				st.Body = string(raw)
			} else {
				if m, ok := doc.(map[string]any); ok && strings.HasPrefix(rq.path, "/admin/api/posts") {
					if mod, ok := m["modified"].(string); ok {
						modified = mod
					}
				}
				if m, ok := doc.(map[string]any); ok && rq.save != "" {
					if id, ok := m["id"].(float64); ok {
						vars[rq.save] = int64(id)
					}
				}
				st.Body = normalizeGolden(doc)
			}
		}
		if rq.save != "" && vars[rq.save] == 0 {
			t.Fatalf("step %q: could not capture id (status %d): %s", rq.name, rec.Code, rec.Body.String())
		}
		steps = append(steps, st)
	}
	return steps
}

// TestBuiltinGolden pins built-in post, page, category and tag behavior,
// without and with a content: registry. Both recordings come from the
// unmodified pre-M17 tree. The with-registry file is separate because the
// admin terms list may legitimately differ once registered entries exist.
func TestBuiltinGolden(t *testing.T) {
	for _, tc := range []struct {
		name         string
		withRegistry bool
		file         string
	}{
		{"WithoutRegistry", false, "without_registry.json"},
		{"WithRegistry", true, "with_registry.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.MarshalIndent(runBuiltinGolden(t, tc.withRegistry), "", "  ")
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			got = append(got, '\n')
			path := filepath.Join("testdata", "builtin_golden", tc.file)
			if *updateBuiltinGolden {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update on the unmodified tree): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("built-in behavior changed vs %s;\nregenerate only if the change is intended and approved.\n%s",
					path, firstDiff(string(want), string(got)))
			}
		})
	}
}

// firstDiff reports the first differing line of two texts.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("first difference at line %d:\n  want: %s\n  got:  %s", i+1, a, b)
		}
	}
	return "no difference"
}
