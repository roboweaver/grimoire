package domain

import (
	"context"
	"testing"
)

// adminFake is a compile-time witness that the new additive read/count ports
// have the shapes M3 depends on. It also lets the zero-value AdminPostFilter be
// exercised (empty Types/Statuses = "no filter").
type adminFake struct {
	posts []Post
	count int
}

func (f adminFake) ListForAdmin(_ context.Context, filter AdminPostFilter) ([]Post, error) {
	// Honor Limit so the fake behaves like a real paginated read.
	if filter.Limit > 0 && filter.Limit < len(f.posts) {
		return f.posts[:filter.Limit], nil
	}
	return f.posts, nil
}

func (f adminFake) CountForAdmin(_ context.Context, _ AdminPostFilter) (int, error) {
	return f.count, nil
}

func (adminFake) Authors(_ context.Context) ([]AuthorOption, error) {
	return []AuthorOption{{ID: 1, DisplayName: "Admin"}}, nil
}

func (f adminFake) CountByStatus(_ context.Context, _, _ string) (int, error) { return f.count, nil }
func (f adminFake) CountUsers(_ context.Context) (int, error)                 { return f.count, nil }
func (f adminFake) CountTerms(_ context.Context, _ string) (int, error)       { return f.count, nil }

// Compile-time interface satisfaction for the new ports.
var (
	_ AdminPostRepository = adminFake{}
	_ PostCounter         = adminFake{}
	_ UserCounter         = adminFake{}
	_ TermCounter         = adminFake{}
)

func TestAdminPostFilterZeroValue(t *testing.T) {
	var f AdminPostFilter
	if f.Types != nil || f.Statuses != nil {
		t.Fatalf("zero AdminPostFilter should have nil slices, got %+v", f)
	}
	if f.Limit != 0 || f.Offset != 0 {
		t.Fatalf("zero AdminPostFilter should have zero paging, got %+v", f)
	}
}

func TestAdminPortsUsable(t *testing.T) {
	ctx := context.Background()
	fake := adminFake{
		posts: []Post{{ID: 1, Type: "post", Status: "publish"}, {ID: 2, Type: "page", Status: "draft"}},
		count: 2,
	}
	got, err := fake.ListForAdmin(ctx, AdminPostFilter{Limit: 1})
	if err != nil {
		t.Fatalf("ListForAdmin: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Limit not honored: got %d", len(got))
	}
	if n, _ := fake.CountForAdmin(ctx, AdminPostFilter{}); n != 2 {
		t.Fatalf("CountForAdmin = %d, want 2", n)
	}
	if n, _ := fake.CountByStatus(ctx, "post", "publish"); n != 2 {
		t.Fatalf("CountByStatus = %d, want 2", n)
	}
	if n, _ := fake.CountUsers(ctx); n != 2 {
		t.Fatalf("CountUsers = %d, want 2", n)
	}
	if n, _ := fake.CountTerms(ctx, "category"); n != 2 {
		t.Fatalf("CountTerms = %d, want 2", n)
	}
}
