package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// fakeAdminData backs the admin read-service tests. Each closure lets a test
// override behavior; unset closures return zero values.
type fakeAdminData struct {
	list       func(domain.AdminPostFilter) ([]domain.Post, error)
	count      func(domain.AdminPostFilter) (int, error)
	authors    func() ([]domain.AuthorOption, error)
	byStatus   func(typ, status string) (int, error)
	countUsers func() (int, error)
	countTerms func(taxonomy string) (int, error)
	postByID   func(id int64) (domain.Post, error)
	userByID   func(id int64) (domain.User, error)
}

func (f *fakeAdminData) ListForAdmin(_ context.Context, flt domain.AdminPostFilter) ([]domain.Post, error) {
	return f.list(flt)
}
func (f *fakeAdminData) CountForAdmin(_ context.Context, flt domain.AdminPostFilter) (int, error) {
	return f.count(flt)
}
func (f *fakeAdminData) Authors(_ context.Context) ([]domain.AuthorOption, error) {
	return f.authors()
}
func (f *fakeAdminData) CountByStatus(_ context.Context, typ, status string) (int, error) {
	return f.byStatus(typ, status)
}
func (f *fakeAdminData) CountUsers(_ context.Context) (int, error) { return f.countUsers() }
func (f *fakeAdminData) CountTerms(_ context.Context, taxonomy string) (int, error) {
	return f.countTerms(taxonomy)
}
func (f *fakeAdminData) ByID(_ context.Context, id int64) (domain.Post, error) {
	return f.postByID(id)
}

// fakeUserReader isolates the user ByID dependency used for display names.
type fakeUserReader struct {
	byID func(id int64) (domain.User, error)
}

func (f *fakeUserReader) ByID(_ context.Context, id int64) (domain.User, error) { return f.byID(id) }

func newAdminService(data *fakeAdminData, users *fakeUserReader) *AdminService {
	return NewAdminService(data, data, data, data, data, users)
}

func TestAdminServiceListClampsAndPaginates(t *testing.T) {
	var gotFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list: func(f domain.AdminPostFilter) ([]domain.Post, error) {
			gotFilter = f
			return []domain.Post{{ID: 3}, {ID: 2}}, nil
		},
		count: func(domain.AdminPostFilter) (int, error) { return 25, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})

	// perPage over the max is clamped to MaxPerPage; page below 1 becomes 1.
	got, err := svc.List(context.Background(), 0, 1000, AdminListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter.Limit != MaxPerPage {
		t.Errorf("filter limit = %d, want %d", gotFilter.Limit, MaxPerPage)
	}
	if gotFilter.Offset != 0 {
		t.Errorf("filter offset = %d, want 0", gotFilter.Offset)
	}
	if got.Page != 1 {
		t.Errorf("page = %d, want 1", got.Page)
	}
	if got.PerPage != MaxPerPage {
		t.Errorf("perPage = %d, want %d", got.PerPage, MaxPerPage)
	}
	if got.Total != 25 {
		t.Errorf("total = %d, want 25", got.Total)
	}
	if got.TotalPages != 1 {
		t.Errorf("totalPages = %d, want 1 (25 items / 100 per page)", got.TotalPages)
	}
	if len(got.Items) != 2 {
		t.Errorf("items = %d, want 2", len(got.Items))
	}
}

func TestAdminServiceListTotalPagesRoundsUp(t *testing.T) {
	var gotFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list:  func(f domain.AdminPostFilter) ([]domain.Post, error) { gotFilter = f; return nil, nil },
		count: func(domain.AdminPostFilter) (int, error) { return 25, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})

	got, err := svc.List(context.Background(), 2, 10, AdminListFilter{Type: "post", Status: "draft"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if got.TotalPages != 3 {
		t.Errorf("totalPages = %d, want 3 (25/10 rounded up)", got.TotalPages)
	}
	if got.Page != 2 || got.PerPage != 10 {
		t.Errorf("page/perPage = %d/%d, want 2/10", got.Page, got.PerPage)
	}
	if gotFilter.Offset != 10 {
		t.Errorf("offset = %d, want 10", gotFilter.Offset)
	}
	// single type/status query params become one-element filters.
	if len(gotFilter.Types) != 1 || gotFilter.Types[0] != "post" {
		t.Errorf("types = %v, want [post]", gotFilter.Types)
	}
	if len(gotFilter.Statuses) != 1 || gotFilter.Statuses[0] != "draft" {
		t.Errorf("statuses = %v, want [draft]", gotFilter.Statuses)
	}
	// CountForAdmin must ignore Limit/Offset when computing the total. The list
	// filter carries paging; the count filter must not.
}

func TestAdminServiceListEmptyFiltersLeaveTypesUnset(t *testing.T) {
	var gotFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list:  func(f domain.AdminPostFilter) ([]domain.Post, error) { gotFilter = f; return nil, nil },
		count: func(domain.AdminPostFilter) (int, error) { return 0, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	if _, err := svc.List(context.Background(), 1, 10, AdminListFilter{}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter.Types != nil {
		t.Errorf("types = %v, want nil (repo applies the default)", gotFilter.Types)
	}
	if gotFilter.Statuses != nil {
		t.Errorf("statuses = %v, want nil", gotFilter.Statuses)
	}
}

func TestAdminServiceDetailPropagatesNotFound(t *testing.T) {
	data := &fakeAdminData{
		postByID: func(int64) (domain.Post, error) { return domain.Post{}, domain.ErrNotFound },
	}
	svc := newAdminService(data, &fakeUserReader{})
	if _, err := svc.Detail(context.Background(), 999); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Detail err = %v, want ErrNotFound", err)
	}
}

func TestAdminServiceDetailReturnsPost(t *testing.T) {
	data := &fakeAdminData{
		postByID: func(id int64) (domain.Post, error) {
			return domain.Post{ID: id, Title: "Hello", Type: "post"}, nil
		},
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.Detail(context.Background(), 7)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if got.ID != 7 || got.Title != "Hello" {
		t.Errorf("got %+v", got)
	}
}

func TestAdminServiceStatsAggregates(t *testing.T) {
	data := &fakeAdminData{
		byStatus: func(typ, status string) (int, error) {
			switch {
			case typ == "post" && status == "publish":
				return 3, nil
			case typ == "post" && status == "draft":
				return 1, nil
			case typ == "page" && status == "":
				return 2, nil
			}
			t.Fatalf("unexpected CountByStatus(%q,%q)", typ, status)
			return 0, nil
		},
		countTerms: func(taxonomy string) (int, error) {
			if taxonomy != "category" {
				t.Fatalf("CountTerms(%q), want category", taxonomy)
			}
			return 5, nil
		},
		countUsers: func() (int, error) { return 4, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	want := Stats{PostsPublished: 3, PostsDraft: 1, Pages: 2, Categories: 5, Users: 4}
	if got != want {
		t.Errorf("stats = %+v, want %+v", got, want)
	}
}

func TestAdminServiceDisplayName(t *testing.T) {
	users := &fakeUserReader{
		byID: func(id int64) (domain.User, error) {
			return domain.User{ID: id, Login: "admin", DisplayName: "Ada Admin"}, nil
		},
	}
	svc := newAdminService(&fakeAdminData{}, users)
	name, err := svc.DisplayName(context.Background(), 1)
	if err != nil {
		t.Fatalf("DisplayName: %v", err)
	}
	if name != "Ada Admin" {
		t.Errorf("name = %q, want Ada Admin", name)
	}
}

func TestAdminServiceListForwardsSearchToCount(t *testing.T) {
	var gotListFilter, gotCountFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list: func(f domain.AdminPostFilter) ([]domain.Post, error) {
			gotListFilter = f
			if f.Search == "hello" {
				return []domain.Post{{ID: 1, Title: "hello"}}, nil
			}
			return []domain.Post{{ID: 1, Title: "hello"}, {ID: 2, Title: "other"}}, nil
		},
		count: func(f domain.AdminPostFilter) (int, error) {
			gotCountFilter = f
			if f.Search == "hello" {
				return 1, nil
			}
			return 2, nil
		},
	}
	svc := newAdminService(data, &fakeUserReader{})

	all, err := svc.List(context.Background(), 1, 10, AdminListFilter{})
	if err != nil {
		t.Fatalf("List (unfiltered): %v", err)
	}
	if all.Total != 2 || all.TotalPages != 1 {
		t.Fatalf("unfiltered Total/TotalPages = %d/%d, want 2/1", all.Total, all.TotalPages)
	}

	filtered, err := svc.List(context.Background(), 1, 10, AdminListFilter{Search: "hello"})
	if err != nil {
		t.Fatalf("List (Search=hello): %v", err)
	}
	if len(filtered.Items) != 1 {
		t.Fatalf("filtered Items = %d, want 1", len(filtered.Items))
	}
	// The regression this test guards: before the fix, List rebuilt the count
	// filter from only Types/Statuses, so a Search-filtered CountForAdmin call
	// never saw Search and Total stayed 2 (the unfiltered count) instead of 1.
	if filtered.Total != 1 || filtered.TotalPages != 1 {
		t.Fatalf("Search-filtered Total/TotalPages = %d/%d, want 1/1 (Search must reach CountForAdmin)", filtered.Total, filtered.TotalPages)
	}
	if gotCountFilter.Search != "hello" {
		t.Fatalf("CountForAdmin did not receive Search: %+v", gotCountFilter)
	}
	if gotListFilter.Search != "hello" {
		t.Fatalf("ListForAdmin did not receive Search: %+v", gotListFilter)
	}
}

func TestAdminServiceListMissingFilterFieldsMeansUnfiltered(t *testing.T) {
	data := &fakeAdminData{
		list:  func(domain.AdminPostFilter) ([]domain.Post, error) { return []domain.Post{{ID: 1}, {ID: 2}}, nil },
		count: func(domain.AdminPostFilter) (int, error) { return 2, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.List(context.Background(), 1, 10, AdminListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got.Items) != 2 || got.Total != 2 {
		t.Fatalf("zero-value AdminListFilter should return all posts, got %d items / total %d", len(got.Items), got.Total)
	}
}

func TestAdminServiceAuthorsDelegates(t *testing.T) {
	data := &fakeAdminData{
		authors: func() ([]domain.AuthorOption, error) {
			return []domain.AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
		},
	}
	svc := newAdminService(data, &fakeUserReader{})
	got, err := svc.Authors(context.Background())
	if err != nil {
		t.Fatalf("Authors: %v", err)
	}
	if len(got) != 1 || got[0].DisplayName != "Admin" {
		t.Fatalf("Authors = %+v", got)
	}
}

func TestAdminServiceListForwardsAuthor(t *testing.T) {
	var gotFilter domain.AdminPostFilter
	data := &fakeAdminData{
		list: func(f domain.AdminPostFilter) ([]domain.Post, error) {
			gotFilter = f
			return []domain.Post{{ID: 1, Author: 7, Title: "a"}}, nil
		},
		count: func(domain.AdminPostFilter) (int, error) { return 1, nil },
	}
	svc := newAdminService(data, &fakeUserReader{})
	if _, err := svc.List(context.Background(), 1, 10, AdminListFilter{Author: 7}); err != nil {
		t.Fatalf("List: %v", err)
	}
	if gotFilter.Author != 7 {
		t.Fatalf("ListForAdmin did not receive Author: %+v", gotFilter)
	}
}
