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
	got, err := svc.List(context.Background(), 0, 1000, "", "")
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

	got, err := svc.List(context.Background(), 2, 10, "post", "draft")
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
	if _, err := svc.List(context.Background(), 1, 10, "", ""); err != nil {
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
