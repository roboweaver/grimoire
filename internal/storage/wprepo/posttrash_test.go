package wprepo

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

func TestTrashedName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", "__trashed"},
		{"simple", "hello", "hello__trashed"},
		{"191 bytes kept whole", strings.Repeat("a", 191), strings.Repeat("a", 191) + "__trashed"},
		{"192 bytes cut to 191", strings.Repeat("a", 192), strings.Repeat("a", 191) + "__trashed"},
		{
			"cut inside %E2 backs up before the percent",
			strings.Repeat("a", 189) + "%E2" + "tail",
			strings.Repeat("a", 189) + "__trashed",
		},
		{
			"cut inside second of two escapes",
			strings.Repeat("a", 187) + "%C3%A9" + "x",
			strings.Repeat("a", 187) + "%C3" + "__trashed",
		},
		{
			"escape ending exactly at 191 is kept",
			strings.Repeat("a", 188) + "%C3" + "tail",
			strings.Repeat("a", 188) + "%C3" + "__trashed",
		},
		{
			"multibyte percent-encoded name never splits an escape",
			strings.Repeat("%C3%A9", 40),
			strings.Repeat("%C3%A9", 31) + "%C3" + "__trashed",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := trashedName(tc.in); got != tc.want {
				t.Fatalf("trashedName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// Feature: trash-and-restore, Property 1: Trashed names have the WordPress shape
//
// Validates: Requirements 1.2
func TestPropertyTrashedName(t *testing.T) {
	const suffix = "__trashed"
	prop := func(in string) bool {
		out := trashedName(in)
		if !strings.HasSuffix(out, suffix) {
			return false
		}
		base := strings.TrimSuffix(out, suffix)
		if len(base) > 191 || !strings.HasPrefix(in, base) {
			return false
		}
		if len(in) <= 191 {
			return base == in
		}
		// The cut must not fall inside a %XX escape of the input.
		for i := 0; i+2 < len(in); i++ {
			if in[i] == '%' && isHex(in[i+1]) && isHex(in[i+2]) && i < len(base) && len(base) < i+3 {
				return false
			}
		}
		return true
	}
	alphabet := "ab-%0123456789ABCDEF"
	cfg := &quick.Config{
		MaxCount: 200,
		Rand:     rand.New(rand.NewSource(1)),
		Values: func(args []reflect.Value, r *rand.Rand) {
			b := make([]byte, r.Intn(300))
			for i := range b {
				b[i] = alphabet[r.Intn(len(alphabet))]
			}
			args[0] = reflect.ValueOf(string(b))
		},
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Fatal(err)
	}
}

// ptaSnapshot renders every row the trasher can touch (posts, postmeta,
// comments, relationships, term counts) as one string, so two snapshots are
// equal exactly when nothing changed. Tables named in skip are not read (they
// have been dropped to inject a failure).
func ptaSnapshot(t *testing.T, repo *PostRepo, skip ...string) string {
	t.Helper()
	ctx := context.Background()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	queries := []struct{ table, q string }{
		{"posts", `SELECT "ID", post_status, post_name, post_modified, post_modified_gmt, post_parent FROM wp_posts ORDER BY "ID"`},
		{"postmeta", `SELECT post_id, meta_key, meta_value FROM wp_postmeta ORDER BY meta_id`},
		{"comments", `SELECT "comment_ID", comment_post_ID, comment_approved FROM wp_comments ORDER BY "comment_ID"`},
		{"term_relationships", `SELECT object_id, term_taxonomy_id FROM wp_term_relationships ORDER BY object_id, term_taxonomy_id`},
		{"term_taxonomy", `SELECT term_taxonomy_id, taxonomy, count FROM wp_term_taxonomy ORDER BY term_taxonomy_id`},
	}
	var b strings.Builder
	for _, qq := range queries {
		if skipped[qq.table] {
			continue
		}
		rows, err := repo.db.QueryContext(ctx, qq.q)
		if err != nil {
			t.Fatalf("snapshot %s: %v", qq.table, err)
		}
		cols, _ := rows.Columns()
		fmt.Fprintf(&b, "[%s]\n", qq.table)
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatalf("snapshot scan %s: %v", qq.table, err)
			}
			fmt.Fprintln(&b, vals...)
		}
		rows.Close()
	}
	return b.String()
}

func ptaExec(t *testing.T, repo *PostRepo, q string, args ...any) {
	t.Helper()
	if _, err := repo.db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// TestTrashPostAtomicOnFailure: the comments table is dropped, so the comment
// step fails after the postmeta inserts, the status and name update have run.
// The method returns an error and every row, meta row and count is unchanged.
func TestTrashPostAtomicOnFailure(t *testing.T) {
	repo := newPostDependentsRepo(t)
	ptaExec(t, repo, `INSERT INTO wp_posts ("ID", post_type, post_status, post_name, post_modified, post_modified_gmt) VALUES (1, 'post', 'publish', 'hello', '2001-02-03 04:05:06', '2001-02-03 04:05:06')`)
	ptaExec(t, repo, `INSERT INTO wp_posts ("ID", post_type, post_status, post_name) VALUES (2, 'post', 'publish', 'sibling')`)
	ptaExec(t, repo, `INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (1, 'k', 'v')`)
	ptaExec(t, repo, `INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES (1, '_wp_trash_meta_status', 'stale')`)
	ptaExec(t, repo, `INSERT INTO wp_terms (term_id, name, slug) VALUES (10, 'c', 'c')`)
	ptaExec(t, repo, `INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (10, 10, 'category', 7)`)
	ptaExec(t, repo, `INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (1, 10), (2, 10)`)
	ptaExec(t, repo, `DROP TABLE wp_comments`)
	before := ptaSnapshot(t, repo, "comments")

	if _, err := repo.TrashPost(context.Background(), 1, time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)); err == nil {
		t.Fatal("TrashPost with the comments table dropped = nil, want an error")
	}
	if after := ptaSnapshot(t, repo, "comments"); after != before {
		t.Errorf("rows changed after a failed TrashPost:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestRestorePostAtomicOnFailure: term_taxonomy is dropped, so the final
// recount fails after the status, name, meta and comment writes have run. The
// method returns an error and every row, meta row and comment is unchanged.
func TestRestorePostAtomicOnFailure(t *testing.T) {
	repo := newPostDependentsRepo(t)
	ptaExec(t, repo, `INSERT INTO wp_posts ("ID", post_type, post_status, post_name) VALUES (1, 'post', 'trash', 'hello__trashed')`)
	ptaExec(t, repo, `INSERT INTO wp_postmeta (post_id, meta_key, meta_value) VALUES
		(1, '_wp_trash_meta_status', 'publish'), (1, '_wp_trash_meta_time', '1714979289'),
		(1, '_wp_desired_post_slug', 'hello'), (1, '_wp_trash_meta_comments_status', 'a:1:{i:5;s:1:"1";}')`)
	ptaExec(t, repo, `INSERT INTO wp_comments ("comment_ID", comment_post_ID, comment_approved) VALUES (5, 1, 'post-trashed')`)
	ptaExec(t, repo, `INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (1, 10)`)
	ptaExec(t, repo, `DROP TABLE wp_term_taxonomy`)
	before := ptaSnapshot(t, repo, "term_taxonomy")

	err := repo.RestorePost(context.Background(), 1, domain.RestoreRequest{
		Status: "publish", At: time.Date(2024, 5, 6, 8, 8, 9, 0, time.UTC),
	})
	if err == nil {
		t.Fatal("RestorePost with term_taxonomy dropped = nil, want an error")
	}
	if after := ptaSnapshot(t, repo, "term_taxonomy"); after != before {
		t.Errorf("rows changed after a failed RestorePost:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}
