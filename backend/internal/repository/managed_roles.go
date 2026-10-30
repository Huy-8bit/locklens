package repository

import (
	"context"

	"github.com/locklens/locklens/internal/repository/queries"
)

// ManagedRole is a login role LockLens created on a monitored cluster.
// The password is encrypted with the master key.
type ManagedRole struct {
	SystemIdentifier   string
	Purpose            string // "monitor" or "reader"
	RoleName           string
	PasswordCiphertext []byte
}

// ManagedRoles returns the roles stored for a cluster, keyed by purpose.
func (s *Store) ManagedRoles(ctx context.Context, systemIdentifier string) (map[string]ManagedRole, error) {
	rows, err := s.pool.Query(ctx, queries.ManagedRoles, systemIdentifier)
	if err != nil {
		return nil, s.red.Error(err)
	}
	defer rows.Close()
	out := map[string]ManagedRole{}
	for rows.Next() {
		var m ManagedRole
		if err := rows.Scan(&m.SystemIdentifier, &m.Purpose, &m.RoleName, &m.PasswordCiphertext); err != nil {
			return nil, s.red.Error(err)
		}
		out[m.Purpose] = m
	}
	return out, s.red.Error(rows.Err())
}

// PutManagedRole stores or replaces the credentials of one managed role.
func (s *Store) PutManagedRole(ctx context.Context, m ManagedRole) error {
	_, err := s.pool.Exec(ctx, queries.PutManagedRole,
		m.SystemIdentifier, m.Purpose, m.RoleName, m.PasswordCiphertext)
	return s.red.Error(err)
}
