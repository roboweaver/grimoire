package content

import (
	"context"
	"strconv"
	"time"

	"github.com/roboweaver/grimoire/internal/auth"
	"github.com/roboweaver/grimoire/internal/auth/password"
	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/sanitize"
)

// UserService creates and administers users: it hashes passwords with bcrypt and
// records role assignments in usermeta as WordPress does ({prefix}capabilities
// PHP-serialized plus the legacy {prefix}user_level integer).
type UserService struct {
	users  domain.UserRepository
	meta   domain.UserMetaRepository
	prefix string
	// policy is the write-boundary content policy applied to the free-text
	// description field (Req 4). It defaults to sanitize.New() in the
	// constructor and is NEVER nil: an unwired service sanitizes rather than
	// silently passing input through, matching MediaWriteService /
	// PostWriteService / the Server (fail closed, Req 11.1/11.3).
	policy *sanitize.Policy
}

// UserServiceOption follows the MediaWriteOption style.
type UserServiceOption func(*UserService)

// WithUserContentPolicy wires the shared *sanitize.Policy (in production the
// process-wide sanitize.New()) into UserService. When omitted, the constructor's
// sanitize.New() default still applies, so the service never silently skips
// sanitization of description (Req 4, 11.1).
func WithUserContentPolicy(p *sanitize.Policy) UserServiceOption {
	return func(s *UserService) { s.policy = p }
}

// NewUserService constructs a UserService. prefix is the table/meta prefix
// (e.g. "wp_") used to build the capabilities and user_level meta keys. The
// content policy defaults to sanitize.New() (never nil / fail closed); pass
// WithUserContentPolicy to share the process-wide policy. The variadic option is
// additive, so the existing CLI/e2e three-argument call sites compile unchanged
// and get the default-policy behavior.
func NewUserService(u domain.UserRepository, m domain.UserMetaRepository, prefix string, opts ...UserServiceOption) *UserService {
	s := &UserService{users: u, meta: m, prefix: prefix, policy: sanitize.New()}
	for _, opt := range opts {
		opt(s)
	}
	return s
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

// UserProfileUpdate is the partial-update request for the profile fields
// UpdateProfile owns: name, email, url, slug, and description. Every field is a
// pointer so UpdateProfile distinguishes "omitted" (nil — leave the stored value
// untouched, Req 7.2) from "explicitly set to empty", exactly as
// restPostWriteRequest / MediaMetaUpdate do. Password and roles are NOT here;
// they flow through SetPassword / SetRoles (Req 3.5). Slug maps to user_nicename
// (Req 7.3) and Description to the prefixless {prefix}usermeta "description" key
// (D7).
type UserProfileUpdate struct {
	Name        *string // display_name
	Email       *string // user_email
	URL         *string // user_url
	Slug        *string // user_nicename
	Description *string // {prefix}usermeta description (D7)
}

// UpdateProfile authorizes and applies a user profile edit. It loads the stored
// user by ID (Req 7.1); evaluates the self-vs-others capability against THAT id
// versus actor.UserID (Req 5.3-5.5, 11.2) — never a body value; sanitizes a
// supplied description through the Policy at the actor's tier (Req 4); persists
// the changed users-table fields through UserRepository.UpdateProfile and a
// changed description through UserMetaRepository.Set with key "description"
// (Req 3.1, 3.4, D7). On any policy error, capability failure, or missing row it
// writes nothing (fail closed, Req 11.3). Returns ErrForbidden on a capability
// failure and domain.ErrNotFound when id resolves to no user (Req 7.4).
func (s *UserService) UpdateProfile(ctx context.Context, actor auth.Principal, id int64, upd UserProfileUpdate) (domain.User, error) {
	if _, err := s.users.ByID(ctx, id); err != nil {
		return domain.User{}, err // domain.ErrNotFound bubbles up (Req 7.4)
	}
	// Self-vs-others: own record needs no capability; another's needs edit_users
	// (Req 5.3, 5.4, D2). Mirrors SetPassword's rule exactly.
	if actor.UserID != id && !auth.CanEditUsers(actor) {
		return domain.User{}, ErrForbidden
	}

	fields := domain.UserProfileFields{} // all-nil = no users-table write
	if upd.Name != nil {
		fields.DisplayName = upd.Name
	}
	if upd.Email != nil {
		fields.Email = upd.Email
	}
	if upd.URL != nil {
		fields.URL = upd.URL
	}
	if upd.Slug != nil {
		fields.Nicename = upd.Slug // Req 7.3
	}

	// description is the one free-text field the Policy governs (Req 4.1, D8).
	// Finding 2 / D8: a user bio is sanitized under the post_content FieldKind —
	// there is no bespoke "user description" kind. PostContent gives tier B to a
	// normal writer and byte-identical tier C to an unfiltered_html holder, which
	// is exactly the D8 behavior we want for a profile bio.
	var sanitizedDesc *string
	if upd.Description != nil {
		writer := sanitize.For(actor) // Req 4.3; tier C when actor holds unfiltered_html (Req 4.2)
		v, err := s.policy.Sanitize(sanitize.PostContent, writer, *upd.Description)
		if err != nil {
			return domain.User{}, err // fail closed → handler maps to 400 (Req 4.4)
		}
		sanitizedDesc = &v
	}

	// Only touch the users row when a users-table field was supplied. This is the
	// cross-package equivalent of UserProfileFields.any() (unexported in domain):
	// a description-only request issues no empty users-table UPDATE (Req 7.2).
	if fields.DisplayName != nil || fields.Email != nil || fields.URL != nil || fields.Nicename != nil {
		if err := s.users.UpdateProfile(ctx, id, fields); err != nil {
			return domain.User{}, err
		}
	}
	if sanitizedDesc != nil {
		if err := s.meta.Set(ctx, id, "description", *sanitizedDesc); err != nil { // D7: prefixless key
			return domain.User{}, err
		}
	}
	return s.users.ByID(ctx, id) // re-read so the handler envelopes the stored values (Req 7.6)
}

// Delete authorizes and performs a user hard delete. It requires delete_users
// (ErrForbidden otherwise, nothing deleted); the self-delete 403 and the
// reassign-target existence check are the handler's responsibility, so by the
// time Delete runs the actor is authorized and reassignTo is already validated.
// It loads the stored user as "previous" (bubbling domain.ErrNotFound) before
// delegating the transactional reassign/delete to UserRepository.Delete.
func (s *UserService) Delete(ctx context.Context, actor auth.Principal, id int64, reassignTo int64) (domain.User, error) {
	if !auth.CanDeleteUsers(actor) {
		return domain.User{}, ErrForbidden
	}
	prev, err := s.users.ByID(ctx, id)
	if err != nil {
		return domain.User{}, err
	}
	if err := s.users.Delete(ctx, id, reassignTo); err != nil {
		return domain.User{}, err
	}
	return prev, nil
}
