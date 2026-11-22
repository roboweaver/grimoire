package routing_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/roboweaver/grimoire/internal/config"
	"github.com/roboweaver/grimoire/internal/routing"
	"github.com/roboweaver/grimoire/internal/storage"
)

// TestRealWordPressDateQuery validates M16's ?m= parser against the dates of the
// 25 newest posts in the real WordPress database (read-only), under the same
// single gate as the other realdb tests: GRIMOIRE_TEST_WP_DSN and
// GRIMOIRE_TEST_WP_PREFIX. No environment variable is added (Req 14.5).
//
//	GRIMOIRE_TEST_WP_DSN='wordpress:PASS@tcp(127.0.0.1:3306)/wordpress' \
//	GRIMOIRE_TEST_WP_PREFIX=accuweaver \
//	go test ./internal/routing/ -run TestRealWordPressDateQuery -v -count=1
//
// For each post, the year, year-month and year-month-day strings are built with
// fmt.Sprintf from the stored date (no timezone shift). ParseDateQuery must
// accept each, the DateRef must equal the one Classify returns for the same
// date's path form (when the site's structure serves date archives), and
// DateQuery must rebuild the string. Every statement issued is a SELECT.
func TestRealWordPressDateQuery(t *testing.T) {
	dsn := os.Getenv("GRIMOIRE_TEST_WP_DSN")
	if dsn == "" {
		t.Skip("set GRIMOIRE_TEST_WP_DSN to run the real WordPress ?m= validation")
	}
	prefix := os.Getenv("GRIMOIRE_TEST_WP_PREFIX")
	if prefix == "" {
		prefix = "accuweaver"
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]+$`).MatchString(prefix) {
		t.Fatalf("GRIMOIRE_TEST_WP_PREFIX %q is not a plain table prefix", prefix)
	}
	ctx := context.Background()
	repos, err := storage.New(config.DatabaseConfig{Vendor: "mysql", DSN: dsn, TablePrefix: prefix})
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer repos.Close()

	structure, err := repos.Options.Get(ctx, "permalink_structure")
	if err != nil {
		t.Fatalf("read permalink_structure: %v", err)
	}
	rawCategoryBase, _ := repos.Options.Get(ctx, "category_base")
	rawTagBase, _ := repos.Options.Get(ctx, "tag_base")
	s, parseErr := routing.Parse(structure, rawCategoryBase, rawTagBase)
	usable := parseErr == nil && strings.TrimSpace(structure) != ""
	pathForm := usable && !s.Flat
	if !pathForm {
		t.Logf("structure %q serves no date archives; path-form comparison skipped", structure)
	}
	front := realWPFront(structure, usable)
	dateFront := append([]string(nil), front...)
	if realWPDatePrefixed(structure) {
		dateFront = append(dateFront, "date")
	}

	posts, err := repos.Posts.RecentPosts(ctx, 25, 0)
	if err != nil {
		t.Fatalf("RecentPosts: %v", err)
	}
	if len(posts) == 0 {
		t.Skip("the target database has no published posts")
	}

	checked := 0
	for _, p := range posts {
		y, m, d := p.Date.Year(), int(p.Date.Month()), p.Date.Day()
		forms := []struct {
			q    string
			segs []string
			want routing.DateRef
		}{
			{fmt.Sprintf("%04d", y), []string{fmt.Sprintf("%04d", y)}, routing.DateRef{Year: y}},
			{fmt.Sprintf("%04d%02d", y, m), []string{fmt.Sprintf("%04d", y), fmt.Sprintf("%02d", m)},
				routing.DateRef{Year: y, Month: m}},
			{fmt.Sprintf("%04d%02d%02d", y, m, d),
				[]string{fmt.Sprintf("%04d", y), fmt.Sprintf("%02d", m), fmt.Sprintf("%02d", d)},
				routing.DateRef{Year: y, Month: m, Day: d}},
		}
		for _, f := range forms {
			got, ok := routing.ParseDateQuery(f.q)
			if !ok {
				t.Errorf("post %d: ParseDateQuery(%q) rejected a real stored date", p.ID, f.q)
				continue
			}
			if got != f.want {
				t.Errorf("post %d: ParseDateQuery(%q) = %+v, want %+v", p.ID, f.q, got, f.want)
			}
			if rebuilt := routing.DateQuery(got); rebuilt != f.q {
				t.Errorf("post %d: DateQuery(%+v) = %q, want %q", p.ID, got, rebuilt, f.q)
			}
			if pathForm {
				path := "/" + strings.Join(append(append([]string(nil), dateFront...), f.segs...), "/")
				tg := s.Classify(path)
				if tg.Kind != routing.KindDate {
					t.Errorf("post %d: Classify(%q).Kind = %v, want KindDate", p.ID, path, tg.Kind)
				} else if tg.Date != got {
					t.Errorf("post %d: Classify(%q).Date = %+v, ParseDateQuery(%q) = %+v",
						p.ID, path, tg.Date, f.q, got)
				}
			}
			checked++
		}
	}
	t.Logf("checked %d ?m= strings from %d real posts (path-form comparison: %v)",
		checked, len(posts), pathForm)
}
