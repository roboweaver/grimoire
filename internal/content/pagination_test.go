package content

import "testing"

func TestClampReturnsClampedPage(t *testing.T) {
	cases := []struct {
		name                            string
		page, perPage                   int
		wantLimit, wantOffset, wantPage int
	}{
		{"defaults", 0, 0, 10, 0, 1},
		{"negative page clamps to 1", -5, 5, 5, 0, 1},
		{"page 3", 3, 10, 10, 20, 3},
		{"perPage capped", 1, 500, 100, 0, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, offset, page := clamp(tc.page, tc.perPage)
			if limit != tc.wantLimit || offset != tc.wantOffset || page != tc.wantPage {
				t.Fatalf("clamp(%d,%d) = (%d,%d,%d), want (%d,%d,%d)",
					tc.page, tc.perPage, limit, offset, page, tc.wantLimit, tc.wantOffset, tc.wantPage)
			}
		})
	}
}

func TestNewPageZeroTotalHasZeroTotalPages(t *testing.T) {
	p := newPage(1, 10, 0)
	if p.TotalPages != 0 {
		t.Fatalf("TotalPages = %d, want 0 for Total=0", p.TotalPages)
	}
	if p.Page != 1 || p.PerPage != 10 || p.Total != 0 {
		t.Fatalf("unexpected Page fields: %+v", p)
	}
}

func TestNewPageComputesTotalPages(t *testing.T) {
	p := newPage(2, 10, 25)
	if p.TotalPages != 3 {
		t.Fatalf("TotalPages = %d, want 3 for Total=25 PerPage=10", p.TotalPages)
	}
}

func TestTotalPagesZeroWhenTotalZero(t *testing.T) {
	if got := TotalPages(0, 10); got != 0 {
		t.Fatalf("TotalPages(0, 10) = %d, want 0", got)
	}
}

func TestTotalPagesComputes(t *testing.T) {
	if got := TotalPages(25, 10); got != 3 {
		t.Fatalf("TotalPages(25, 10) = %d, want 3", got)
	}
}
