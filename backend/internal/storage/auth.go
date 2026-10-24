package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminUser is the single administrator account.
type AdminUser struct {
	ID           int64
	Username     string
	PasswordHash string
}

// UpsertAdmin creates or updates the administrator. LockLens supports one
// admin: any other admin row is removed so the environment stays the single
// source of truth for credentials.
func (s *Store) UpsertAdmin(ctx context.Context, username, passwordHash string) (int64, error) {
	var id int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
INSERT INTO admin_users (username, password_hash) VALUES ($1, $2)
ON CONFLICT (username) DO UPDATE SET password_hash = EXCLUDED.password_hash,
    updated_at = CASE WHEN admin_users.password_hash = EXCLUDED.password_hash THEN admin_users.updated_at ELSE now() END
RETURNING id`, username, passwordHash).Scan(&id); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "DELETE FROM admin_users WHERE id <> $1", id)
		return err
	})
	return id, s.red.Error(err)
}

// AdminByUsername looks up the administrator.
func (s *Store) AdminByUsername(ctx context.Context, username string) (AdminUser, error) {
	var u AdminUser
	err := s.pool.QueryRow(ctx, "SELECT id, username, password_hash FROM admin_users WHERE username = $1", username).
		Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return u, ErrNotFound
	}
	return u, s.red.Error(err)
}

// AdminPasswordHash returns the stored hash for a user ID.
func (s *Store) AdminPasswordHash(ctx context.Context, userID int64) (string, error) {
	var h string
	err := s.pool.QueryRow(ctx, "SELECT password_hash FROM admin_users WHERE id = $1", userID).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return h, s.red.Error(err)
}

// AuthSession is a logged-in browser session.
type AuthSession struct {
	UserID    int64
	Username  string
	CSRFToken string
	ExpiresAt time.Time
}

// CreateSession stores a session identified by the hash of its token.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, csrf string, expires time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO auth_sessions (token_hash, user_id, csrf_token, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, userID, csrf, expires.UTC())
	return s.red.Error(err)
}

// SessionByHash returns a non-expired session and slides its expiry.
func (s *Store) SessionByHash(ctx context.Context, tokenHash []byte, idle time.Duration) (AuthSession, error) {
	var sess AuthSession
	err := s.pool.QueryRow(ctx, `
UPDATE auth_sessions AS a
SET last_seen_at = now(), expires_at = greatest(a.expires_at, now() + $2::interval)
FROM admin_users AS u
WHERE a.token_hash = $1 AND a.expires_at > now() AND u.id = a.user_id
RETURNING a.user_id, u.username, a.csrf_token, a.expires_at`, tokenHash, idle).
		Scan(&sess.UserID, &sess.Username, &sess.CSRFToken, &sess.ExpiresAt)
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

// DeleteAllSessions logs everybody out, for example after the admin password
// changed.
func (s *Store) DeleteAllSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, "DELETE FROM auth_sessions")
	return s.red.Error(err)
}
