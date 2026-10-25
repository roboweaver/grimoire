package content

import (
	"context"
	"errors"
	"testing"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/domain"
)

// TestUserServiceDeleteCapabilityGate covers Req 8.1: delete_users is required
// (administrator-only by default); a non-administrator gets ErrForbidden and
// nothing is deleted.
func TestUserServiceDeleteCapabilityGate(t *testing.T) {
	svc, ur, _ := newUserService()
	ur.byID[5] = domain.User{ID: 5, Login: "victim"}

	// editor lacks delete_users.
	if _, err := svc.Delete(context.Background(), actor(auth.RoleEditor, 1), 5, 0); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-admin delete = %v, want ErrForbidden", err)
	}
	if len(ur.deleteCalls) != 0 {
		t.Errorf("forbidden delete must not reach the repo; calls=%v", ur.deleteCalls)
	}

	// administrator holds delete_users.
	if _, err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), 5, 0); err != nil {
		t.Fatalf("admin delete = %v, want nil", err)
	}
	if len(ur.deleteCalls) != 1 || ur.deleteCalls[0].id != 5 {
		t.Errorf("admin delete should reach the repo once for id 5; calls=%v", ur.deleteCalls)
	}
}

// TestUserServiceDeleteNotFound covers Req 5.4/9.5: an unknown id bubbles
// domain.ErrNotFound (from the previous-load) and the repo delete is not called.
func TestUserServiceDeleteNotFound(t *testing.T) {
	svc, ur, _ := newUserService()
	_, err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), 404, 0)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("unknown id delete = %v, want domain.ErrNotFound", err)
	}
	if len(ur.deleteCalls) != 0 {
		t.Errorf("not-found delete must not reach the repo; calls=%v", ur.deleteCalls)
	}
}

// TestUserServiceDeletePreviousAndReassignPassthrough covers Req 5.3/9: the
// returned previous equals the stored user captured before delete, and
// reassignTo is passed straight through for both the 0 and >0 branches.
func TestUserServiceDeletePreviousAndReassignPassthrough(t *testing.T) {
	t.Run("reassign=0 passthrough + previous", func(t *testing.T) {
		svc, ur, _ := newUserService()
		ur.byID[9] = domain.User{ID: 9, Login: "gone", Email: "g@example.com"}
		prev, err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), 9, 0)
		if err != nil {
			t.Fatalf("delete: %v", err)
		}
		if prev.ID != 9 || prev.Login != "gone" {
			t.Errorf("previous = %+v, want stored user id 9 'gone'", prev)
		}
		if len(ur.deleteCalls) != 1 || ur.deleteCalls[0].reassignTo != 0 {
			t.Errorf("reassignTo not passed through as 0; calls=%v", ur.deleteCalls)
		}
	})

	t.Run("reassign=id passthrough", func(t *testing.T) {
		svc, ur, _ := newUserService()
		ur.byID[9] = domain.User{ID: 9, Login: "gone"}
		if _, err := svc.Delete(context.Background(), actor(auth.RoleAdministrator, 1), 9, 3); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if len(ur.deleteCalls) != 1 || ur.deleteCalls[0].reassignTo != 3 {
			t.Errorf("reassignTo not passed through as 3; calls=%v", ur.deleteCalls)
		}
	})
}
