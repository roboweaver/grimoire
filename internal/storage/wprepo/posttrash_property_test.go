package wprepo

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/quick"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
)

// Property tests for the trasher run against one migrated SQLite database each,
// with a fresh high id and a fresh post type per iteration so rows left by an
// earlier iteration never share a slug scope with the next.

const ptpIterations = 100

var (
	ptpStatuses     = []string{"publish", "draft", "pending", "private", "future"}
	ptpApproved     = []string{"0", "1", "spam", "trash"}
	ptpTermTaxIDs   = []int64{100, 101, 102}
	ptpTermTaxonomy = []string{"category", "product_cat", "post_tag"}
	ptpTrashAt      = time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
)

// ptpName draws a post_name that does not end in __trashed: empty, short,
// 191 bytes, 195 bytes, multibyte, and WordPress-style percent-encoded.
func ptpName(r *rand.Rand) string {
	switch r.Intn(8) {
	case 0:
		return ""
	case 1:
		return strings.Repeat("a", 191)
	case 2:
		return strings.Repeat("b", 195)
	case 3:
		return "h\u00e9llo-w\u00f6rld"
	case 4:
		return "caf%c3%a9-%e2%80%94"
	case 5:
		return strings.Repeat("%c3%a9", 31) // 186 bytes, ends on an escape boundary
	default:
		return "post-" + strconv.Itoa(r.Intn(1000))
	}
}

type ptpScenario struct {
	Status   string
	Name     string
	Comments []string
	Terms    []int64 // term_taxonomy_ids the row is related to
}

func ptpGenScenario(r *rand.Rand) ptpScenario {
	s := ptpScenario{Status: ptpStatuses[r.Intn(len(ptpStatuses))], Name: ptpName(r)}
	for i, n := 0, r.Intn(5); i < n; i++ {
		s.Comments = append(s.Comments, ptpApproved[r.Intn(len(ptpApproved))])
	}
	for _, tt := range ptpTermTaxIDs {
		if r.Intn(2) == 0 {
			s.Terms = append(s.Terms, tt)
		}
	}
	return s
}

// ptpWorld is one migrated database plus the id counter of its iterations.
type ptpWorld struct {
	repo *PostRepo
	n    int64
}

func newPtpWorld(t *testing.T) *ptpWorld {
	t.Helper()
	repo := newPostDependentsRepo(t)
	for i, tt := range ptpTermTaxIDs {
		ptaExec(t, repo, `INSERT INTO wp_terms (term_id, name, slug) VALUES (?, ?, ?)`, tt, ptpTermTaxonomy[i], ptpTermTaxonomy[i])
		ptaExec(t, repo, `INSERT INTO wp_term_taxonomy (term_taxonomy_id, term_id, taxonomy, count) VALUES (?, ?, ?, 0)`, tt, tt, ptpTermTaxonomy[i])
		// An anchor post keeps every term's count above zero.
		ptaExec(t, repo, `INSERT INTO wp_posts ("ID", post_type, post_status, post_name) VALUES (?, 'anchor', 'publish', ?)`, 50+int64(i), "anchor-"+strconv.Itoa(i))
		ptaExec(t, repo, `INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (?, ?)`, 50+int64(i), tt)
	}
	return &ptpWorld{repo: repo, n: 0}
}

// next returns a fresh post id and a post type used by this iteration only.
func (w *ptpWorld) next() (id int64, typ string) {
	w.n++
	return 100000 + w.n*100, "ptp" + strconv.FormatInt(w.n, 10)
}

func (w *ptpWorld) insertPost(t *testing.T, id int64, typ, status, name string, parent int64) {
	t.Helper()
	ptaExec(t, w.repo, `INSERT INTO wp_posts ("ID", post_author, post_content, post_title, post_status, post_name, post_type, post_parent, menu_order, guid, post_modified, post_modified_gmt)
		VALUES (?, 1, 'body', 'T', ?, ?, ?, ?, 4, ?, '2001-02-03 04:05:06', '2001-02-03 04:05:06')`,
		id, status, name, typ, parent, "http://example.test/?p="+strconv.FormatInt(id, 10))
}

// recount makes every term count equal its relationship count.
func (w *ptpWorld) recount(t *testing.T) {
	t.Helper()
	ptaExec(t, w.repo, `UPDATE wp_term_taxonomy SET count = (SELECT COUNT(*) FROM wp_term_relationships tr WHERE tr.term_taxonomy_id = wp_term_taxonomy.term_taxonomy_id)`)
}

func (w *ptpWorld) rows(t *testing.T, q string, args ...any) [][]string {
	t.Helper()
	rs, err := w.repo.db.QueryContext(context.Background(), q, args...)
	if err != nil {
		t.Fatalf("query %q: %v", q, err)
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out [][]string
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			t.Fatalf("scan %q: %v", q, err)
		}
		line := make([]string, len(cols))
		for i, v := range vals {
			line[i] = fmt.Sprint(v)
		}
		out = append(out, line)
	}
	return out
}

// postColumns is every column of the row except the two modified timestamps.
func (w *ptpWorld) postColumns(t *testing.T, id int64) []string {
	t.Helper()
	r := w.rows(t, `SELECT post_author, post_date, post_date_gmt, post_content, post_title, post_excerpt, post_status, post_name,
		post_type, comment_status, ping_status, post_password, post_parent, post_mime_type, menu_order, guid FROM wp_posts WHERE "ID" = ?`, id)
	if len(r) != 1 {
		t.Fatalf("post %d: %d rows, want 1", id, len(r))
	}
	return r[0]
}

func (w *ptpWorld) approvals(t *testing.T, postID int64) [][]string {
	t.Helper()
	return w.rows(t, `SELECT "comment_ID", comment_approved FROM wp_comments WHERE comment_post_ID = ? ORDER BY "comment_ID"`, postID)
}

func (w *ptpWorld) counts(t *testing.T) [][]string {
	t.Helper()
	return w.rows(t, `SELECT term_taxonomy_id, count FROM wp_term_taxonomy ORDER BY term_taxonomy_id`)
}

func (w *ptpWorld) trashMeta(t *testing.T, id int64) [][]string {
	t.Helper()
	return w.rows(t, `SELECT meta_key FROM wp_postmeta WHERE post_id = ? AND (meta_key LIKE '\_wp\_trash\_meta\_%' ESCAPE '\' OR meta_key = '_wp_desired_post_slug')`, id)
}

// seed inserts a live row of the scenario with its comments and relationships
// and brings the term counts in line with the relationships.
func (w *ptpWorld) seed(t *testing.T, s ptpScenario) (id int64, typ string) {
	t.Helper()
	id, typ = w.next()
	w.insertPost(t, id, typ, s.Status, s.Name, 0)
	for i, a := range s.Comments {
		ptaExec(t, w.repo, `INSERT INTO wp_comments ("comment_ID", comment_post_ID, comment_approved) VALUES (?, ?, ?)`, id+int64(i)+1, id, a)
	}
	for _, tt := range s.Terms {
		ptaExec(t, w.repo, `INSERT INTO wp_term_relationships (object_id, term_taxonomy_id) VALUES (?, ?)`, id, tt)
	}
	w.recount(t)
	return id, typ
}

func ptpCheck(t *testing.T, prop any, gen func(r *rand.Rand) reflect.Value, seed int64) {
	t.Helper()
	cfg := &quick.Config{
		MaxCount: ptpIterations,
		Rand:     rand.New(rand.NewSource(seed)),
		Values: func(args []reflect.Value, r *rand.Rand) {
			args[0] = gen(r)
		},
	}
	if err := quick.Check(prop, cfg); err != nil {
		t.Fatal(err)
	}
}

// Feature: trash-and-restore, Property 2: Trash then restore round-trips a live row
//
// Validates: Requirements 1.1, 1.4, 2.1, 2.2, 4.1, 4.3, 4.7, 5.3
func TestPropertyTrashRestoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	w := newPtpWorld(t)
	prop := func(s ptpScenario) bool {
		id, _ := w.seed(t, s)
		wantCols := w.postColumns(t, id)
		wantApprovals := w.approvals(t, id)
		wantCounts := w.counts(t)

		prev, err := w.repo.TrashPost(ctx, id, ptpTrashAt)
		if err != nil {
			t.Logf("TrashPost(%+v): %v", s, err)
			return false
		}
		if prev != s.Status {
			t.Logf("previous status = %q, want %q", prev, s.Status)
			return false
		}
		if err := w.repo.RestorePost(ctx, id, domain.RestoreRequest{Status: prev, At: ptpTrashAt.Add(time.Hour)}); err != nil {
			t.Logf("RestorePost(%+v): %v", s, err)
			return false
		}
		if got := w.postColumns(t, id); !reflect.DeepEqual(got, wantCols) {
			t.Logf("columns after round trip (%+v):\n got  %q\n want %q", s, got, wantCols)
			return false
		}
		if got := w.approvals(t, id); !reflect.DeepEqual(got, wantApprovals) {
			t.Logf("comment approvals = %v, want %v", got, wantApprovals)
			return false
		}
		if got := w.trashMeta(t, id); len(got) != 0 {
			t.Logf("trash meta left behind: %v", got)
			return false
		}
		if got := w.counts(t); !reflect.DeepEqual(got, wantCounts) {
			t.Logf("term counts = %v, want %v", got, wantCounts)
			return false
		}
		return true
	}
	ptpCheck(t, prop, func(r *rand.Rand) reflect.Value { return reflect.ValueOf(ptpGenScenario(r)) }, 2)
}

// Feature: trash-and-restore, Property 3: A second trash changes nothing
//
// Validates: Requirements 1.7
func TestPropertySecondTrashChangesNothing(t *testing.T) {
	ctx := context.Background()
	w := newPtpWorld(t)
	prop := func(s ptpScenario) bool {
		id, _ := w.seed(t, s)
		if _, err := w.repo.TrashPost(ctx, id, ptpTrashAt); err != nil {
			t.Logf("TrashPost(%+v): %v", s, err)
			return false
		}
		before := ptaSnapshot(t, w.repo)
		_, err := w.repo.TrashPost(ctx, id, ptpTrashAt.Add(48*time.Hour))
		if !errors.Is(err, domain.ErrAlreadyTrashed) {
			t.Logf("second TrashPost(%+v) = %v, want ErrAlreadyTrashed", s, err)
			return false
		}
		if after := ptaSnapshot(t, w.repo); after != before {
			t.Logf("second TrashPost changed rows (%+v)", s)
			return false
		}
		return true
	}
	ptpCheck(t, prop, func(r *rand.Rand) reflect.Value { return reflect.ValueOf(ptpGenScenario(r)) }, 3)
}

type ptpSlugScenario struct {
	Name         string
	Held         int // number of live rows holding n, n-2, ..., n-Held
	Hierarchical bool
	Status       string
}

// Feature: trash-and-restore, Property 4: A restored name is free
//
// Validates: Requirements 2.4
func TestPropertyRestoredNameIsFree(t *testing.T) {
	ctx := context.Background()
	w := newPtpWorld(t)
	prop := func(s ptpSlugScenario) bool {
		id, typ := w.next()
		w.insertPost(t, id, typ, "publish", s.Name, 0)
		if _, err := w.repo.TrashPost(ctx, id, ptpTrashAt); err != nil {
			t.Logf("TrashPost(%q): %v", s.Name, err)
			return false
		}
		// Live rows of the same type take n, then n-2 ... n-Held.
		for k := 0; k < s.Held; k++ {
			name := s.Name
			if k > 0 {
				name = s.Name + "-" + strconv.Itoa(k+1)
			}
			w.insertPost(t, id+int64(k)+1, typ, s.Status, name, 0)
		}
		err := w.repo.RestorePost(ctx, id, domain.RestoreRequest{
			Status: "publish", HierarchicalScope: s.Hierarchical, At: ptpTrashAt.Add(time.Hour),
		})
		if err != nil {
			t.Logf("RestorePost(%+v): %v", s, err)
			return false
		}
		got := w.rows(t, `SELECT post_name FROM wp_posts WHERE "ID" = ?`, id)[0][0]
		if len(got) > 200 {
			t.Logf("restored name is %d bytes, want at most 200", len(got))
			return false
		}
		if held := w.rows(t, `SELECT COUNT(*) FROM wp_posts WHERE post_name = ? AND post_type = ? AND "ID" <> ? AND post_status <> 'trash'`, got, typ, id)[0][0]; held != "0" {
			t.Logf("restored name %q is held by %s other live rows (%+v)", got, held, s)
			return false
		}
		want := s.Name
		if s.Held > 0 {
			want = s.Name + "-" + strconv.Itoa(s.Held+1)
		}
		if len(want) <= 200 && got != want {
			t.Logf("restored name = %q, want %q (%+v)", got, want, s)
			return false
		}
		return true
	}
	gen := func(r *rand.Rand) reflect.Value {
		var name string
		switch r.Intn(4) {
		case 0:
			name = strings.Repeat("n", 1+r.Intn(190))
		case 1:
			name = strings.Repeat("n", 199)
		default:
			name = "slug-" + strconv.Itoa(r.Intn(100))
		}
		return reflect.ValueOf(ptpSlugScenario{
			Name:         name,
			Held:         r.Intn(5),
			Hierarchical: r.Intn(2) == 0,
			Status:       []string{"publish", "draft", "private"}[r.Intn(3)],
		})
	}
	ptpCheck(t, prop, gen, 4)
}
