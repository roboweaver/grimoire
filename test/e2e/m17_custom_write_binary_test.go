package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/content"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage"
	"github.com/roboweaver/grimoire/internal/storage/migrate"
	"github.com/roboweaver/grimoire/internal/storage/seed"
)

// syncBuffer is a goroutine-safe bytes.Buffer for capturing process output.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// TestM17BinaryCustomWrite builds the real ./cmd/grimoire binary and drives
// custom type and taxonomy writes over HTTP against a temp SQLite database
// (Req 17.2, 17.4; D14, D16). It exists to catch a wiring omission in main.go
// that buildHandler-level tests cannot see, such as the scheduler losing the
// capability suffixes of registered types.
func TestM17BinaryCustomWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("built-binary smoke test skipped under -short")
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go toolchain unavailable: ", err)
	}
	ctx := context.Background()
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()

	// Build the real binary.
	bin := filepath.Join(tmp, "grimoire")
	build := exec.Command(goBin, "build", "-o", bin, "./cmd/grimoire")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/grimoire: %v\n%s", err, out)
	}

	// Migrate and seed the database, create the administrator and one due
	// "future" product row, then release the file before the binary opens it.
	dsn := "file:" + filepath.Join(tmp, "grimoire.db") + "?_pragma=busy_timeout(5000)"
	dbCfg := config.DatabaseConfig{Vendor: "sqlite", DSN: dsn, TablePrefix: "wp_"}
	repos, err := storage.New(dbCfg)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	migFS, err := storage.MigrationsFS(dbCfg.Vendor)
	if err != nil {
		t.Fatalf("MigrationsFS: %v", err)
	}
	if _, err := migrate.Apply(ctx, repos.DB(), migFS, dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("migrate.Apply: %v", err)
	}
	if err := seed.Run(ctx, repos.DB(), dbCfg.Vendor, dbCfg.TablePrefix); err != nil {
		t.Fatalf("seed.Run: %v", err)
	}
	const adminLogin, adminPass = "root", "correct-horse-battery-staple"
	users := content.NewUserService(repos.Users, repos.UserMeta, dbCfg.TablePrefix)
	adminID, err := users.Bootstrap(ctx, domain.User{
		Login: adminLogin, Nicename: adminLogin, DisplayName: "Admin", Email: "admin@example.test",
	}, adminPass, append([]string{auth.RoleAdministrator}, productCaps()...)...)
	if err != nil {
		t.Fatalf("bootstrap admin: %v", err)
	}
	past := time.Now().Add(-time.Hour)
	if _, err := repos.PostWriter.Create(ctx, domain.Post{
		Author: adminID, Title: "Scheduled widget", Status: "future", Type: "product",
		Slug: "scheduled-widget", Date: past, DateGMT: past,
	}); err != nil {
		t.Fatalf("create due future product: %v", err)
	}
	if err := repos.Close(); err != nil {
		t.Fatalf("close repos: %v", err)
	}

	// Free port, then the config with a content: section.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()
	cfgPath := filepath.Join(tmp, "grimoire.yaml")
	cfgYAML := fmt.Sprintf(`server:
  addr: %q
theme: default
database:
  vendor: sqlite
  dsn: %q
  table_prefix: wp_
session:
  cookie_name: grimoire_session
media:
  uploads_dir: %q
scheduler:
  interval_seconds: 1
content:
  post_types:
    - name: product
      public: true
      slug: product
      capability_type: product
      taxonomies: [genre]
    - name: guide
      public: true
      slug: guide
      hierarchical: true
      capability_type: guide
      taxonomies: [genre]
  taxonomies:
    - name: genre
      public: true
      slug: genre
      hierarchical: true
      object_types: [product, guide]
`, addr, dsn, filepath.Join(tmp, "uploads"))
	if err := os.WriteFile(cfgPath, []byte(cfgYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	// Run the binary.
	logs := &syncBuffer{}
	cmd := exec.Command(bin, "-config", cfgPath, "-themes", filepath.Join(repoRoot, "themes"))
	cmd.Dir = tmp
	cmd.Stdout, cmd.Stderr = logs, logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start binary: %v", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			_ = cmd.Process.Kill()
			<-exited
		}
		if t.Failed() {
			t.Logf("binary output:\n%s", logs.String())
		}
	})

	base := "http://" + addr
	jar, _ := cookiejar.New(nil)
	client := &http.Client{
		Jar:     jar,
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// Wait for readiness.
	ready := false
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		select {
		case err := <-exited:
			t.Fatalf("binary exited early: %v\n%s", err, logs.String())
		default:
		}
		if resp, err := client.Get(base + "/healthz"); err == nil {
			resp.Body.Close()
			ready = true
			break
		}
	}
	if !ready {
		t.Fatalf("binary never became ready\n%s", logs.String())
	}

	// Log in with the real form flow, then read the session CSRF token.
	resp, err := client.Get(base + "/login")
	if err != nil {
		t.Fatalf("GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	csrf := formInputs(string(body))["csrf_token"]
	if resp.StatusCode != http.StatusOK || csrf == "" {
		t.Fatalf("GET /login = %d, csrf %q", resp.StatusCode, csrf)
	}
	resp, err = client.PostForm(base+"/login", url.Values{
		"log": {adminLogin}, "pwd": {adminPass}, "csrf_token": {csrf}, "redirect": {"/"},
	})
	if err != nil {
		t.Fatalf("POST /login: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("POST /login = %d, want 303", resp.StatusCode)
	}
	var sess struct {
		CSRFToken string `json:"csrfToken"`
	}
	if status, raw := doBinary(t, client, "", http.MethodGet, base+"/admin/api/session", ""); status != http.StatusOK {
		t.Fatalf("GET /admin/api/session = %d: %s", status, raw)
	} else if err := json.Unmarshal(raw, &sess); err != nil || sess.CSRFToken == "" {
		t.Fatalf("session csrf missing: %v %s", err, raw)
	}
	call := func(method, path, payload string, want int, what string) []byte {
		t.Helper()
		status, raw := doBinary(t, client, sess.CSRFToken, method, base+path, payload)
		if status != want {
			t.Fatalf("%s: %s %s = %d, want %d; body: %s\nlogs:\n%s", what, method, path, status, want, raw, logs.String())
		}
		return raw
	}
	type item struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
		Type string `json:"type"`
	}
	decode := func(raw []byte) item {
		t.Helper()
		var it item
		if err := json.Unmarshal(raw, &it); err != nil {
			t.Fatalf("decode %q: %v", raw, err)
		}
		return it
	}

	// REST: genre term, then a product carrying it.
	term := decode(call(http.MethodPost, "/wp-json/wp/v2/genre", `{"name":"Fiction","slug":"fiction"}`, http.StatusCreated, "create genre"))
	prod := decode(call(http.MethodPost, "/wp-json/wp/v2/product",
		fmt.Sprintf(`{"title":"Widget","status":"publish","genre":[%d]}`, term.ID), http.StatusCreated, "create product"))
	if prod.Type != "product" || prod.Slug != "widget" {
		t.Fatalf("created product type/slug = %q/%q, want product/widget", prod.Type, prod.Slug)
	}
	restPath := fmt.Sprintf("/wp-json/wp/v2/product/%d", prod.ID)
	call(http.MethodGet, restPath, "", http.StatusOK, "REST read product")
	call(http.MethodGet, "/product/widget", "", http.StatusOK, "public URL")
	call(http.MethodGet, "/genre/fiction", "", http.StatusOK, "genre archive")
	call(http.MethodPatch, restPath, `{"title":"Widget Pro"}`, http.StatusOK, "REST update product")

	// Admin API: a second product through /admin/api/posts.
	adm := decode(call(http.MethodPost, "/admin/api/posts",
		`{"title":"Gadget","type":"product","status":"publish"}`, http.StatusCreated, "admin create product"))
	admPath := fmt.Sprintf("/admin/api/posts/%d", adm.ID)
	call(http.MethodDelete, admPath, "", http.StatusNoContent, "admin delete product")

	// Delete the product, then the term.
	call(http.MethodDelete, restPath, "", http.StatusOK, "REST delete product")
	call(http.MethodGet, "/product/widget", "", http.StatusNotFound, "public URL after delete")
	call(http.MethodDelete, fmt.Sprintf("/wp-json/wp/v2/genre/%d?force=true", term.ID), "", http.StatusOK, "delete genre")

	// The due "future" custom row is published by the scheduler: its public URL
	// goes from 404 to 200 and no scheduler error is logged (Req 17.2; D14).
	published := false
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		status, _ := doBinary(t, client, "", http.MethodGet, base+"/product/scheduled-widget", "")
		if status == http.StatusOK {
			published = true
			break
		}
	}
	if !published {
		t.Errorf("due future product never published by the scheduler\nlogs:\n%s", logs.String())
	}
	if out := logs.String(); strings.Contains(out, "scheduler:") {
		t.Errorf("binary logged a scheduler error:\n%s", out)
	}
}

// doBinary issues one request with the client's session cookie and, when csrf
// is non-empty, the X-CSRF-Token header. It returns the status and body.
func doBinary(t *testing.T, c *http.Client, csrf, method, target, payload string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, target, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if payload != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

// productCaps is the seven-verb capability family of the product type. The
// built-in administrator role carries no custom-type capabilities (D13), so the
// smoke administrator is granted them explicitly, as an operator would.
func productCaps() []string {
	var caps []string
	for _, v := range []string{"edit_", "edit_others_", "edit_published_", "publish_", "delete_", "delete_others_", "delete_published_"} {
		caps = append(caps, v+"products")
	}
	return caps
}
