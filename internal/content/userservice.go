package content

import (
	"context"
	"strconv"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
)

// UserService creates and administers users: it hashes passwords with bcrypt and
// records role assignments in usermeta as WordPress does ({prefix}capabilities
// PHP-serialized plus the legacy {prefix}user_level integer).
type UserService struct {
	users  domain.UserRepository
	meta   domain.UserMetaRepository
	prefix string
}

// NewUserService constructs a UserService. prefix is the table/meta prefix
// (e.g. "wp_") used to build the capabilities and user_level meta keys.
func NewUserService(u domain.UserRepository, m domain.UserMetaRepository, prefix string) *UserService {
	return &UserService{users: u, meta: m, prefix: prefix}
}

func (s *UserService) capsKey() string  { return s.prefix + "capabilities" }
func (s *UserService) levelKey() string { return s.prefix + "user_level" }

// Bootstrap creates a user without an authorization check. It exists for the CLI
// first-admin bootstrap, where no authenticated principal exists yet. All other
// callers must use Create.
func (s *UserService) Bootstrap(ctx context.Context, u domain.User, plainPassword string, roles ...string) (int64, error) {
	return s.provision(ctx, u, plainPassword, roles...)
}

// Create authorizes (create_users) and provisions a new user with the given
// password and roles.
func (s *UserService) Create(ctx context.Context, actor auth.Principal, u domain.User, plainPassword string, roles ...string) (int64, error) {
	if !auth.CanCreateUsers(actor) {
		return 0, ErrForbidden
	}
	return s.provision(ctx, u, plainPassword, roles...)
}

// provision hashes the password, inserts the user, and writes role meta. It
// performs no authorization and is shared by Bootstrap and Create.
func (s *UserService) provision(ctx context.Context, u domain.User, plainPassword string, roles ...string) (int64, error) {
	hash, err := password.Hash(plainPassword)
	if err != nil {
		return 0, err
	}
	u.Pass = hash
	if u.Registered.IsZero() {
		u.Registered = time.Now().UTC()
	}
	id, err := s.users.Create(ctx, u)
	if err != nil {
		return 0, err
	}
	if err := s.writeRoles(ctx, id, roles...); err != nil {
		return 0, err
	}
	return id, nil
}

// SetRoles authorizes (edit_users) and replaces a user's role assignment.
func (s *UserService) SetRoles(ctx context.Context, actor auth.Principal, userID int64, roles ...string) error {
	if !auth.CanEditUsers(actor) {
		return ErrForbidden
	}
	return s.writeRoles(ctx, userID, roles...)
}

// SetPassword changes a user's password. It is permitted when the actor holds
// edit_users or is changing their own password. The new password is bcrypt
// hashed before storage.
func (s *UserService) SetPassword(ctx context.Context, actor auth.Principal, userID int64, plainPassword string) error {
	if actor.UserID != userID && !auth.CanEditUsers(actor) {
		return ErrForbidden
	}
	hash, err := password.Hash(plainPassword)
	if err != nil {
		return err
	}
	return s.users.UpdatePass(ctx, userID, hash)
}

// writeRoles serializes roles into the capabilities meta and records the derived
// legacy user_level.
func (s *UserService) writeRoles(ctx context.Context, userID int64, roles ...string) error {
	caps, err := auth.SerializeCapabilities(roles...)
	if err != nil {
		return err
	}
	if err := s.meta.Set(ctx, userID, s.capsKey(), caps); err != nil {
		return err
	}
	return s.meta.Set(ctx, userID, s.levelKey(), strconv.Itoa(auth.UserLevel(roles...)))
}
