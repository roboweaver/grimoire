package wprepo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/roboweaver/grimoire/internal/domain"
	"github.com/roboweaver/grimoire/internal/storage/rebind"
	"github.com/uptrace/bun"
)

// compile-time interface check.
var _ domain.SessionRepository = (*SessionRepo)(nil)

type sessionRow struct {
	ID        string `bun:"id"`
	UserID    int64  `bun:"user_id"`
	CSRFToken string `bun:"csrf_token"`
	Created   string `bun:"created"`
	Expires   string `bun:"expires"`
}

func (r sessionRow) toDomain() domain.Session {
	return domain.Session{
		ID:        r.ID,
		UserID:    r.UserID,
		CSRFToken: r.CSRFToken,
		Created:   parseTS(r.Created),
		Expires:   parseTS(r.Expires),
	}
}

// SessionRepo persists server-side sessions.
type SessionRepo struct {
	db     *bun.DB
	prefix string
}

// NewSessionRepo builds a SessionRepo bound to db and the table prefix.
func NewSessionRepo(db *bun.DB, prefix string) *SessionRepo {
	return &SessionRepo{db: db, prefix: prefix}
}

// Create inserts a new session row. The id column is a lowercase identifier and
// is caller-supplied (the hashed token), so a plain rebound INSERT is portable.
func (r *SessionRepo) Create(ctx context.Context, s domain.Session) error {
	q := "INSERT INTO " + r.prefix + "sessions (id, user_id, csrf_token, created, expires) VALUES (?, ?, ?, ?, ?)"
	_, err := r.db.ExecContext(ctx, rebind.Rebind(vendorOf(r.db), q),
		s.ID, s.UserID, s.CSRFToken, formatTS(s.Created), formatTS(s.Expires))
	return err
}

// ByID returns a session by its ID (hashed token), or ErrNotFound.
func (r *SessionRepo) ByID(ctx context.Context, id string) (domain.Session, error) {
	var row sessionRow
	err := r.db.NewSelect().
		TableExpr("?", bun.Ident(r.prefix+"sessions")).
		Column("id", "user_id", "csrf_token", "created", "expires").
		Where("id = ?", id).
		Limit(1).
		Scan(ctx, &row)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Session{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Session{}, err
	}
	return row.toDomain(), nil
}

// Touch extends a session's expiry (rolling refresh), or ErrNotFound.
func (r *SessionRepo) Touch(ctx context.Context, id string, expires time.Time) error {
	res, err := r.db.NewUpdate().
		TableExpr("?", bun.Ident(r.prefix+"sessions")).
		Set("expires = ?", formatTS(expires)).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return err
	}
	return errNotFoundIfZero(res)
}

// Delete removes a single session (logout). Deleting a missing session is not an
// error, so logout is idempotent.
func (r *SessionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"sessions")).
		Where("id = ?", id).
		Exec(ctx)
	return err
}

// DeleteByUser removes all of a user's sessions and returns the rows deleted.
func (r *SessionRepo) DeleteByUser(ctx context.Context, userID int64) (int64, error) {
	res, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"sessions")).
		Where("user_id = ?", userID).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteExpired removes sessions that expired before the given time and returns
// the rows deleted. Comparison is lexical over the fixed-width timestamp format.
func (r *SessionRepo) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	res, err := r.db.NewDelete().
		TableExpr("?", bun.Ident(r.prefix+"sessions")).
		Where("expires < ?", formatTS(before)).
		Exec(ctx)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
