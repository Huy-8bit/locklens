package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrConflict is returned when a unique value (a user name) is taken.
var ErrConflict = errors.New("already exists")

// User is a LockLens account. Managed is the administrator configured by
// the environment (LOCKLENS_ADMIN_USER / LOCKLENS_ADMIN_PASSWORD).
type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	Disabled     bool
	Managed      bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
	LastLoginAt  *time.Time
}

const userColumns = "id, username, password_hash, role, disabled, managed, created_at, updated_at, last_login_at"

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.Disabled, &u.Managed, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, err
}

func isUniqueViolation(err error) bool {
	var pg *pgconn.PgError
	return errors.As(err, &pg) && pg.Code == "23505"
}

// UpsertManagedAdmin creates or updates the environment administrator: an
// enabled admin with the given password hash. Earlier environment
// administrators with another user name are removed, so the environment
// stays the single source of truth for that account. Other users are kept.
// It reports whether the stored password hash changed.
func (s *Store) UpsertManagedAdmin(ctx context.Context, username, passwordHash string) (id int64, changed bool, err error) {
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var prev *string
		if err := tx.QueryRow(ctx, "SELECT password_hash FROM users WHERE lower(username) = lower($1)", username).Scan(&prev); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO users (username, password_hash, role, disabled, managed) VALUES ($1, $2, 'admin', false, true)
ON CONFLICT (lower(username)) DO UPDATE SET password_hash = EXCLUDED.password_hash, role = 'admin', disabled = false, managed = true,
    updated_at = CASE WHEN users.password_hash = EXCLUDED.password_hash AND users.role = 'admin' AND NOT users.disabled AND users.managed
                      THEN users.updated_at ELSE now() END
RETURNING id`, username, passwordHash).Scan(&id); err != nil {
			return err
		}
		changed = prev != nil && *prev != passwordHash
		_, err := tx.Exec(ctx, "DELETE FROM users WHERE managed AND id <> $1", id)
		return err
	})
	return id, changed, s.red.Error(err)
}

// UserByUsername looks up a user (case-insensitive).
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.oneUser(scanUser(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE lower(username) = lower($1)", username)))
}

// UserByID looks up a user.
func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	return s.oneUser(scanUser(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM users WHERE id = $1", id)))
}

// oneUser keeps ErrNotFound comparable and redacts other errors.
func (s *Store) oneUser(u User, err error) (User, error) {
	if err == nil || errors.Is(err, ErrNotFound) {
		return u, err
	}
	return u, s.red.Error(err)
}

// ListUsers returns every user, the environment administrator first.
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+userColumns+" FROM users ORDER BY managed DESC, lower(username)")
	if err != nil {
		return nil, s.red.Error(err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, s.red.Error(err)
		}
		out = append(out, u)
	}
	return out, s.red.Error(rows.Err())
}

// CreateUser adds a user. ErrConflict means the name is taken.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash, role string) (User, error) {
	u, err := scanUser(s.pool.QueryRow(ctx, "INSERT INTO users (username, password_hash, role) VALUES ($1, $2, $3) RETURNING "+userColumns,
		strings.TrimSpace(username), passwordHash, role))
	if isUniqueViolation(err) {
		return u, ErrConflict
	}
	return s.oneUser(u, err)
}

// UserChanges are the fields an administrator can change; nil keeps a field.
type UserChanges struct {
	Role         *string
	Disabled     *bool
	PasswordHash *string
}

// UpdateUser changes a user. Disabling a user or changing their password
// ends their sessions; a role change applies to their next request.
func (s *Store) UpdateUser(ctx context.Context, id int64, c UserChanges) (User, error) {
	var u User
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		u, err = scanUser(tx.QueryRow(ctx, `
UPDATE users SET role = coalesce($2, role), disabled = coalesce($3, disabled), password_hash = coalesce($4, password_hash), updated_at = now()
WHERE id = $1 RETURNING `+userColumns, id, c.Role, c.Disabled, c.PasswordHash))
		if err != nil {
			return err
		}
		if (c.Disabled != nil && *c.Disabled) || c.PasswordHash != nil {
			_, err = tx.Exec(ctx, "DELETE FROM auth_sessions WHERE user_id = $1", id)
		}
		return err
	})
	return s.oneUser(u, err)
}

// DeleteUser removes a user and their sessions.
func (s *Store) DeleteUser(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return s.red.Error(err)
}

// TouchLogin records a successful login.
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.pool.Exec(ctx, "UPDATE users SET last_login_at = now() WHERE id = $1", id)
	return s.red.Error(err)
}

// AuthSession is a logged-in browser session.
type AuthSession struct {
	UserID    int64
	Username  string
	Role      string
	Managed   bool
	CSRFToken string
	ExpiresAt time.Time
}

// CreateSession stores a session identified by the hash of its token.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, csrf string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO auth_sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, csrf, expires.UTC())
	return s.red.Error(err)
}

// SessionByHash returns a non-expired session of an enabled user and slides
// its expiry. The role is read on every request, so a role change applies
// immediately.
func (s *Store) SessionByHash(ctx context.Context, tokenHash []byte, idle time.Duration) (AuthSession, error) {
	var sess AuthSession
	err := s.pool.QueryRow(ctx, `
UPDATE auth_sessions AS a
SET last_seen_at = now(), expires_at = greatest(a.expires_at, now() + $2::interval)
FROM users AS u
WHERE a.token_hash = $1 AND a.expires_at > now() AND u.id = a.user_id AND NOT u.disabled
RETURNING a.user_id, u.username, u.role, u.managed, a.csrf_token, a.expires_at`, tokenHash, idle).
		Scan(&sess.UserID, &sess.Username, &sess.Role, &sess.Managed, &sess.CSRFToken, &sess.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return sess, ErrNotFound
	}
	return sess, s.red.Error(err)
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM auth_sessions WHERE token_hash = $1", tokenHash)
	return s.red.Error(err)
}

// DeleteUserSessions logs one user out everywhere.
func (s *Store) DeleteUserSessions(ctx context.Context, userID int64) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM auth_sessions WHERE user_id = $1", userID)
	return s.red.Error(err)
}
