package wprepo

import (
	"context"
	"database/sql"
	"errors"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/uptrace/bun"
)

// compile-time interface checks.
var (
	_ domain.UserRepository     = (*UserRepo)(nil)
	_ domain.NicenameAuditor    = (*UserRepo)(nil)
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

// ByNicename returns a user by user_nicename, or ErrNotFound.
//
// The ORDER BY "ID" ASC is a deliberate divergence from WordPress.
// WP_User::get_data_by( 'slug', ... ) issues the same LIMIT 1 with no ORDER BY
// at all, behind an object cache, so its winner among duplicates is arbitrary
// and can flip on a cache flush with nothing in the database having changed.
// Duplicates are schema-legal: user_nicename carries a *non-unique* index
// (verified against the live WordPress database: Non_unique = 1), even though
// wp_insert_user() prevents them on write by appending numeric suffixes
// (alice, alice-2). Direct SQL, imports, multisite merges, pre-suffix-era
// WordPress and plugins all bypass that write path. grimoire therefore picks
// the lowest ID: there is no shared object cache here to copy WordPress's
// nondeterminism from, unordered row order diverges far more across SQLite,
// MySQL and Postgres than inside WordPress's MySQL-only world, and a
// cross-vendor contract test needs a stable winner to assert. A duplicate is
// not an error — the consequence is the same as WordPress's either way: one
// /author/{nicename} URL can surface only one of the colliding authors, so the
// other's posts are unreachable by that route. grimoire-cli migrate -check
// reports the condition so it is discoverable before the site serves.
func (r *UserRepo) ByNicename(ctx context.Context, nicename string) (domain.User, error) {
	var row userRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Column(userColumns...).
		Where("user_nicename = ?", nicename).
		// bun.Ident("ID"), never a bare ID: Postgres declares the column
		// quoted as "ID", so unquoted it folds to id and fails on that vendor
		// alone (issues #40/#43 were both this defect).
		OrderExpr("? ASC", bun.Ident("ID")).
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

// DuplicateNicenames reports every user_nicename shared by more than one row,
// ordered by nicename. It satisfies domain.NicenameAuditor — the narrow,
// operator-only interface — and is deliberately not part of
// domain.UserRepository: its only caller is "grimoire-cli migrate -check", while
// UserRepository is held by every request path.
//
// One aggregate statement, identical on all three vendors: the winner is
// MIN(ID), which is exactly what ByNicename's ORDER BY "ID" ASC LIMIT 1 picks,
// so the report cannot name a row the read does not serve. No duplicates yields
// an empty slice and a nil error — that is the common case, not a failure.
func (r *UserRepo) DuplicateNicenames(ctx context.Context) ([]domain.NicenameConflict, error) {
	var rows []struct {
		Nicename string `bun:"user_nicename"`
		Count    int    `bun:"n"`
		WinnerID int64  `bun:"winner"`
	}
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Column("user_nicename").
		ColumnExpr("COUNT(*) AS n").
		// bun.Ident("ID"), never a bare ID: Postgres declares the column
		// quoted as "ID", so unquoted it folds to id and fails on that vendor
		// alone (issues #40/#43 were both this defect).
		ColumnExpr("MIN(?) AS winner", bun.Ident("ID")).
		GroupExpr("user_nicename").
		Having("COUNT(*) > 1").
		OrderExpr("user_nicename ASC").
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	conflicts := make([]domain.NicenameConflict, len(rows))
	for i, row := range rows {
		conflicts[i] = domain.NicenameConflict{
			Nicename: row.Nicename,
			Count:    row.Count,
			WinnerID: row.WinnerID,
		}
	}
	return conflicts, nil
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

// List returns users ordered by ID ascending, for REST /users pagination.
func (r *UserRepo) List(ctx context.Context, limit, offset int) ([]domain.User, error) {
	var rows []userRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Column(userColumns...).
		OrderExpr("? ASC", bun.Ident("ID")).
		Limit(limit).
		Offset(offset).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, len(rows))
	for i, row := range rows {
		users[i] = row.toDomain()
	}
	return users, nil
}

// Count returns the total number of users, ignoring limit/offset.
func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	n, err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"users")).
		Count(ctx)
	if err != nil {
		return 0, err
	}
	return int64(n), nil
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
//
// NOTE (concurrency): this UPDATE-then-INSERT is not atomic, and the WordPress
// usermeta schema intentionally has no UNIQUE(user_id, meta_key) constraint —
// WP allows multi-valued meta, so adding one would diverge from the compat
// contract. Two concurrent first-writes for the same (user_id, meta_key) can
// therefore both find zero rows and both insert, leaving duplicate rows. This
// is tolerated: readers resolve duplicates deterministically as last-row-wins
// (Get orders umeta_id DESC LIMIT 1; ByUser applies later rows over earlier
// ones), matching WordPress behavior. A UNIQUE index + native upsert is the
// stronger fix but is deliberately out of scope to preserve WP-schema
// compatibility and migration stability.
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
	_, err = r.db.ExecContext(ctx, q, userID, key, value)
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
