package wprepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

// compile-time interface checks.
var (
	_ domain.UserRepository     = (*UserRepo)(nil)
	_ domain.UserMetaRepository = (*UserMetaRepo)(nil)
)

// userColumns are the users columns selected into a userRow, in WP order.
var userColumns = []string{
	"ID", "user_login", "user_nicename", "display_name", "user_pass",
	"user_email", "user_url", "user_registered", "user_activation_key", "user_status",
}

type userRow struct {
	ID            int64  `bun:"ID"`
	Login         string `bun:"user_login"`
	Nicename      string `bun:"user_nicename"`
	DisplayName   string `bun:"display_name"`
	Pass          string `bun:"user_pass"`
	Email         string `bun:"user_email"`
	URL           string `bun:"user_url"`
	Registered    string `bun:"user_registered"`
	ActivationKey string `bun:"user_activation_key"`
	Status        int    `bun:"user_status"`
}

func (r userRow) toDomain() domain.User {
	return domain.User{
		ID:            r.ID,
		Login:         r.Login,
		Nicename:      r.Nicename,
		DisplayName:   r.DisplayName,
		Pass:          r.Pass,
		Email:         r.Email,
		URL:           r.URL,
		Registered:    parseTS(r.Registered),
		ActivationKey: r.ActivationKey,
		Status:        r.Status,
	}
}

// UserRepo reads and writes users.
type UserRepo struct {
	db     *bun.DB
	prefix string
}

// NewUserRepo builds a UserRepo bound to db and the table prefix.
func NewUserRepo(db *bun.DB, prefix string) *UserRepo { return &UserRepo{db: db, prefix: prefix} }

func (r *UserRepo) selectOne(ctx context.Context, col string, val any) (domain.User, error) {
	var row userRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Column(userColumns...).
		Where("? = ?", bun.Ident(col), val).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	return row.toDomain(), nil
}

// ByLogin returns a user by user_login, or ErrNotFound.
func (r *UserRepo) ByLogin(ctx context.Context, login string) (domain.User, error) {
	return r.selectOne(ctx, "user_login", login)
}

// ByID returns a user by ID, or ErrNotFound.
func (r *UserRepo) ByID(ctx context.Context, id int64) (domain.User, error) {
	return r.selectOne(ctx, "ID", id)
}

// Create inserts a new user and returns its generated ID.
func (r *UserRepo) Create(ctx context.Context, u domain.User) (int64, error) {
	cols := []string{
		"user_login", "user_nicename", "display_name", "user_pass",
		"user_email", "user_url", "user_registered", "user_activation_key", "user_status",
	}
	args := []any{
		u.Login, u.Nicename, u.DisplayName, u.Pass,
		u.Email, u.URL, formatTS(u.Registered), u.ActivationKey, u.Status,
	}
	return insertReturningID(ctx, r.db, vendorOf(r.db), r.prefix+"users", cols, `"ID"`, args...)
}

// UpdatePass replaces the stored password hash for a user, or ErrNotFound.
func (r *UserRepo) UpdatePass(ctx context.Context, id int64, passHash string) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Set("user_pass = ?", passHash).
		Where("? = ?", bun.Ident("ID"), id).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// UserMetaRepo reads and writes single-valued user metadata.
type UserMetaRepo struct {
	db     *bun.DB
	prefix string
}

// NewUserMetaRepo builds a UserMetaRepo bound to db and the table prefix.
func NewUserMetaRepo(db *bun.DB, prefix string) *UserMetaRepo {
	return &UserMetaRepo{db: db, prefix: prefix}
}

// Get returns the value for a user's meta key, or ErrNotFound. When multiple
// rows share the key (legacy data), the most recently inserted wins.
func (r *UserMetaRepo) Get(ctx context.Context, userID int64, key string) (string, error) {
	var value string
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"usermeta")).
		Column("meta_value").
		Where("user_id = ?", userID).
		Where("meta_key = ?", key).
		OrderExpr("umeta_id DESC").
		Limit(1).
		Scan(ctx, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", domain.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return value, nil
}

// Set upserts a single-valued meta row for the user/key pair. Existing rows for
// the key are updated in place; if none exist a new row is inserted.
func (r *UserMetaRepo) Set(ctx context.Context, userID int64, key, value string) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"usermeta")).
		Set("meta_value = ?", value).
		Where("user_id = ?", userID).
		Where("meta_key = ?", key).
		Exec(ctx)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	q := "INSERT INTO " + r.prefix + "usermeta (user_id, meta_key, meta_value) VALUES (?, ?, ?)"
	_, err = r.db.ExecContext(ctx, rebind.Rebind(vendorOf(r.db), q), userID, key, value)
	return err
}

// ByUser returns all single-valued meta for a user keyed by meta_key. Rows with
// a NULL meta_key are skipped; later rows win on duplicate keys.
func (r *UserMetaRepo) ByUser(ctx context.Context, userID int64) (map[string]string, error) {
	var rows []struct {
		Key   sql.NullString `bun:"meta_key"`
		Value sql.NullString `bun:"meta_value"`
	}
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"usermeta")).
		Column("meta_key", "meta_value").
		Where("user_id = ?", userID).
		OrderExpr("umeta_id ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, m := range rows {
		if !m.Key.Valid {
			continue
		}
		out[m.Key.String] = m.Value.String
	}
	return out, nil
}
