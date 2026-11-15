package content

import (
	"context"
	"reflect"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// fakeAuthorTypes records the types AuthorsOfTypes was called with.
type fakeAuthorTypes struct {
	called   bool
	gotTypes []string
	result   []domain.AuthorOption
}

func (f *fakeAuthorTypes) AuthorsOfTypes(_ context.Context, types []string) ([]domain.AuthorOption, error) {
	f.called = true
	f.gotTypes = types
	return f.result, nil
}

// M12 Req 2.3; D9: with WithAuthorTypes set, Authors routes through the
// type-parameterized reader with the configured types.
func TestAdminServiceAuthorTypesRoutesThroughReader(t *testing.T) {
	data := &fakeAdminData{
		authors: func() ([]domain.AuthorOption, error) {
			t.Fatal("default Authors must not be called when WithAuthorTypes is set")
			return nil, nil
		},
	}
	reader := &fakeAuthorTypes{result: []domain.AuthorOption{{ID: 7, DisplayName: "Ursula"}}}
	types := []string{"post", "page", "book"}

	svc := newAdminService(data, &fakeUserReader{}).WithAuthorTypes(reader, types)
	got, err := svc.Authors(context.Background())
	if err != nil {
		t.Fatalf("Authors: %v", err)
	}
	if !reader.called {
		t.Fatal("AuthorsOfTypes was not called")
	}
	if !reflect.DeepEqual(reader.gotTypes, types) {
		t.Fatalf("AuthorsOfTypes types = %v, want %v", reader.gotTypes, types)
	}
	if len(got) != 1 || got[0].ID != 7 {
		t.Fatalf("Authors = %+v", got)
	}
}

// Without WithAuthorTypes, Authors behaves as before (delegates to the
// AdminPostRepository.Authors read).
func TestAdminServiceAuthorTypesUnsetUsesDefault(t *testing.T) {
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
