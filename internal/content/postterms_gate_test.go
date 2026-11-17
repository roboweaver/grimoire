package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/domain"
)

// --- Plan 18 task 3.1 (RED): type gate on SetPostTerms (Req 2.1) ------------
//
// Reuses the ptcFake/ptcService helpers from postterms_custom_test.go. Unlike
// the sibling services, SetPostTerms is already registry-aware; it must refuse
// an unwritable stored type with ErrForbidden before any write.

func TestPostTermsGateUnwritableRowIsForbidden(t *testing.T) {
	for _, typ := range gate3Unwritable {
		f := newPtcFake(domain.Post{ID: 7, Type: typ, Status: "draft", Author: 1})
		svc := ptcService(t, f)
		// Replacing with nil would detach terms; a non-empty set would attach.
		for _, ids := range [][]int64{nil, {1}} {
			err := svc.SetPostTerms(context.Background(), gate3Admin(), 7, "category", ids)
			if !errors.Is(err, ErrForbidden) {
				t.Errorf("type=%s ids=%v err = %v, want ErrForbidden", typ, ids, err)
			}
		}
		if len(f.sets) != 0 {
			t.Errorf("type=%s: PostTermsWriter saw %d call(s), want 0", typ, len(f.sets))
		}
	}
}

func TestPostTermsGateBuiltinAndRegisteredRowsUnchanged(t *testing.T) {
	f := newPtcFake(
		domain.Post{ID: 1, Type: "post", Status: "draft", Author: 5},
		domain.Post{ID: 2, Type: "page", Status: "draft", Author: 5},
		domain.Post{ID: 3, Type: "", Status: "draft", Author: 5},
		ptcProduct(4, 5),
	)
	svc := ptcService(t, f)
	for _, id := range []int64{1, 2, 3} {
		caps := ptcPostsOnly
		if id == 2 {
			// Pages need page capabilities (Req 2.5: built-ins behave as before).
			if err := svc.SetPostTerms(context.Background(), ptcActor(5, ptcPostsOnly...), id, "category", []int64{4}); !errors.Is(err, ErrForbidden) {
				t.Errorf("post-only principal on page: err = %v, want ErrForbidden", err)
			}
			caps = append(append([]string{}, ptcPostsOnly...), "edit_pages", "edit_others_pages")
		}
		if err := svc.SetPostTerms(context.Background(), ptcActor(5, caps...), id, "category", []int64{4}); err != nil {
			t.Errorf("built-in row %d: %v", id, err)
		}
	}
	if err := svc.SetPostTerms(context.Background(), ptcActor(5, ptcProdAuthor...), 4, "genre", []int64{1}); err != nil {
		t.Errorf("registered product row: %v", err)
	}
	if len(f.sets) != 4 {
		t.Errorf("writer calls = %d, want 4", len(f.sets))
	}
}
