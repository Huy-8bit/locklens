package targetdb

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/targetdb/queries"
)

// SetupFacts is what a setup connection may do on a server.
type SetupFacts struct {
	User, Version      string
	VersionNum         int
	Standby, Superuser bool
	// SeesAll: the role sees every session (pg_read_all_stats).
	SeesAll bool
	// Unsafe: the role is administrative or can use server files.
	Unsafe         bool
	StatStatements bool
	// SystemID and ExistingRoles are read for superusers only.
	SystemID      string
	ExistingRoles []string
}

// ReadSetupFacts reads the facts of a setup connection; for a superuser also
// the cluster identifier and which of roles already exist.
func ReadSetupFacts(ctx context.Context, conn *pgx.Conn, roles []string) (SetupFacts, error) {
	var f SetupFacts
	if err := conn.QueryRow(ctx, queries.SetupFacts).
		Scan(&f.User, &f.Version, &f.VersionNum, &f.Standby, &f.Superuser, &f.SeesAll, &f.Unsafe, &f.StatStatements); err != nil {
		return f, err
	}
	if !f.Superuser {
		return f, nil
	}
	if err := conn.QueryRow(ctx, queries.SystemIdentifier).Scan(&f.SystemID); err != nil {
		return f, err
	}
	rows, err := conn.Query(ctx, queries.ExistingRoles, roles)
	if err != nil {
		return f, err
	}
	f.ExistingRoles, err = pgx.CollectRows(rows, pgx.RowTo[string])
	return f, err
}

// RoleStatements are the statements that create (or reset, when they exist)
// the monitoring and reader roles with their passwords and allow them to
// connect to database.
func RoleStatements(database, monitorRole, readerRole string, existing []string, monitorPassword, readerPassword string) []string {
	verb := func(role string) string {
		if slices.Contains(existing, role) {
			return "ALTER"
		}
		return "CREATE"
	}
	return queries.CreateRoles(map[string]string{
		"monitor_verb": verb(monitorRole), "reader_verb": verb(readerRole),
		"monitor": pgx.Identifier{monitorRole}.Sanitize(), "reader": pgx.Identifier{readerRole}.Sanitize(),
		"monitor_password": quoteLiteral(monitorPassword), "reader_password": quoteLiteral(readerPassword),
		"database": pgx.Identifier{database}.Sanitize(),
	})
}

func quoteLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
