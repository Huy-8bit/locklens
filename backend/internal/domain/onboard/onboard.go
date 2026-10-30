// Package onboard adds a PostgreSQL instance from a single connection string.
//
// It inspects the connection and picks one of three setups:
//
//   - direct: the role is not administrative. LockLens monitors with it and,
//     when allowed, uses the same role for the Explorer and the read-only
//     SQL editor.
//   - create_roles: the role is a superuser on a primary. After the
//     administrator confirms the shown statements, LockLens creates two
//     least-privilege login roles (locklens_monitor with pg_monitor,
//     locklens_reader with pg_read_all_data) with generated passwords and
//     stores only those. The administrative connection string is never
//     stored.
//   - reuse_roles: the roles were created earlier for the same cluster
//     (matched by system identifier, so standbys of a primary qualify) and
//     their stored passwords still work on this node.
//
// Nothing else is ever executed with the administrative connection.
package onboard

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/lib/secret"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// Role names LockLens creates.
const (
	MonitorRole = "locklens_monitor"
	ReaderRole  = "locklens_reader"
)

// Setup modes.
const (
	ModeDirect      = "direct"
	ModeCreateRoles = "create_roles"
	ModeReuseRoles  = "reuse_roles"
	ModeUnsupported = "unsupported"
)

// Plan describes what adding a connection string would do.
type Plan struct {
	OK    bool    `json:"ok"`
	Error *string `json:"error"`
	Hint  *string `json:"hint"`

	Mode   string `json:"mode"`
	Reason string `json:"reason"`

	Host            string `json:"host"`
	Port            int    `json:"port"`
	Database        string `json:"database"`
	User            string `json:"user"`
	ServerVersion   string `json:"server_version"`
	IsStandby       bool   `json:"is_standby"`
	Superuser       bool   `json:"superuser"`
	SeesAllSessions bool   `json:"sees_all_sessions"`
	// WorkspaceAllowed says whether the Explorer and SQL editor can use the
	// resulting connection (never with an administrative role).
	WorkspaceAllowed bool `json:"workspace_allowed"`
	PgStatStatements bool `json:"pg_stat_statements"`

	SuggestedName string `json:"suggested_name"`
	// Statements are run by create_roles, with passwords masked.
	Statements []string `json:"statements"`
	// ExistingRoles already exist and get a new password with create_roles.
	ExistingRoles []string `json:"existing_roles"`
	Warnings      []string `json:"warnings"`

	systemIdentifier string
	managed          map[string]repository.ManagedRole
}

// Request adds an instance.
type Request struct {
	DSN         string `json:"dsn"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	BrowseData  bool   `json:"browse_data"`
	RunSQL      bool   `json:"run_sql"`
	// Confirm must be true to run the create_roles statements.
	Confirm bool `json:"confirm"`
}

// Service adds instances from one connection string, creating LockLens's own least-privilege roles when asked.
type Service interface {
	// Inspect connects with the connection string and returns the plan. A
	// connection failure is reported in the plan (OK false), not as an error.
	Inspect(ctx context.Context, dsn string) (Plan, error)
	// Add inspects the connection string again and adds the instance with the
	// resulting plan. It returns the new target and the plan that was applied.
	Add(ctx context.Context, req Request) (targets.View, Plan, error)
}

var _ Service = (*service)(nil)

// service adds instances.
type service struct {
	store     Repositories
	box       *secret.Box
	targets   targets.Service
	workspace workspace.Service
}

// New returns a service.
func New(store Repositories, box *secret.Box, t targets.Service, ws workspace.Service) Service {
	return &service{store: store, box: box, targets: t, workspace: ws}
}

func aad(systemIdentifier, purpose string) []byte {
	return []byte("managed-role:" + systemIdentifier + ":" + purpose)
}

func connectConfig(dsn string) (*pgx.ConnConfig, *config.Redactor, error) {
	_, red, err := config.PoolConfig(dsn, config.Options{})
	if err != nil {
		return nil, nil, &targets.ValidationError{Fields: map[string]string{"dsn": err.Error()}}
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, nil, &targets.ValidationError{Fields: map[string]string{"dsn": "not a valid PostgreSQL connection string"}}
	}
	cfg.ConnectTimeout = 5 * time.Second
	cfg.RuntimeParams["application_name"] = "locklens-setup"
	cfg.RuntimeParams["statement_timeout"] = "10000"
	cfg.RuntimeParams["lock_timeout"] = "3000"
	return cfg, red, nil
}

func suggestName(cfg *pgx.ConnConfig, standby bool) string {
	n := fmt.Sprintf("%s @ %s:%d", cfg.Database, cfg.Host, cfg.Port)
	if standby {
		n += " (standby)"
	}
	if r := []rune(n); len(r) > 100 {
		n = string(r[:100])
	}
	return n
}

// Inspect connects with the connection string and returns the plan. A
// connection failure is reported in the plan (OK false), not as an error.
func (s *service) Inspect(ctx context.Context, dsn string) (Plan, error) {
	cfg, red, err := connectConfig(dsn)
	if err != nil {
		return Plan{}, err
	}
	p := Plan{Host: cfg.Host, Port: int(cfg.Port), Database: cfg.Database, User: cfg.User, Statements: []string{}, ExistingRoles: []string{}, Warnings: []string{}}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, cfg)
	if err != nil {
		msg := strings.Join(strings.Fields(red.String(err.Error())), " ")
		p.Error = &msg
		h := "Check the host, port, database, user, and password, and that pg_hba.conf allows this connection from the LockLens server."
		p.Hint = &h
		return p, nil
	}
	defer conn.Close(context.Background())
	f, err := targetdb.ReadSetupFacts(cctx, conn, []string{MonitorRole, ReaderRole})
	if err != nil {
		msg := strings.Join(strings.Fields(red.String(err.Error())), " ")
		p.Error = &msg
		return p, nil
	}
	p.OK = true
	p.ServerVersion, p.IsStandby, p.Superuser, p.PgStatStatements = f.Version, f.Standby, f.Superuser, f.StatStatements
	p.User = f.User
	p.SuggestedName = suggestName(cfg, f.Standby)
	p.systemIdentifier = f.SystemID
	if !f.StatStatements {
		p.Warnings = append(p.Warnings, "pg_stat_statements is not installed in this database: the Queries pages stay empty until an administrator runs CREATE EXTENSION pg_stat_statements (it also needs shared_preload_libraries).")
	}
	switch {
	case f.VersionNum < 140000:
		p.Mode, p.Reason = ModeUnsupported, "LockLens needs PostgreSQL 14 or later."
		return p, nil
	case !f.Superuser:
		p.Mode = ModeDirect
		p.SeesAllSessions = f.SeesAll
		p.WorkspaceAllowed = !f.Unsafe
		p.Reason = "LockLens monitors with this connection."
		if p.WorkspaceAllowed {
			p.Reason += " The Explorer and SQL editor use it too, in read-only transactions."
		} else {
			p.Warnings = append(p.Warnings, "This role has administrative privileges (for example CREATEROLE or CREATEDB), so the Explorer and SQL editor are not offered with it. Paste a superuser connection to let LockLens create safe roles, or a role without these privileges.")
		}
		if !f.SeesAll {
			p.Warnings = append(p.Warnings, "This role cannot see other users' sessions and queries in full: grant it pg_monitor (or pg_read_all_stats).")
		}
		return p, nil
	}
	p.SeesAllSessions, p.WorkspaceAllowed = true, true
	if len(cfg.Fallbacks) > 0 {
		p.Mode, p.Reason = ModeUnsupported, "Use a connection string with a single host to set up roles automatically."
		return p, nil
	}
	managed, err := s.store.ManagedRoles(ctx, f.SystemID)
	if err != nil {
		return p, err
	}
	p.managed = managed
	if len(managed) == 2 && s.rolesWork(cctx, dsn, managed) {
		p.Mode = ModeReuseRoles
		p.Reason = fmt.Sprintf("This is a superuser. LockLens already created %s and %s for this cluster and reuses them; nothing is changed on the server.", MonitorRole, ReaderRole)
		return p, nil
	}
	if f.Standby {
		p.Mode, p.Reason = ModeUnsupported, "This is a superuser on a standby, which cannot create roles. Add the primary first: LockLens creates its roles there and they reach this standby through replication."
		return p, nil
	}
	p.Mode = ModeCreateRoles
	p.Reason = fmt.Sprintf("This is a superuser. LockLens will not store it: it creates %s (monitoring, pg_monitor) and %s (Explorer and SQL editor, pg_read_all_data, read-only by default) with generated passwords and stores only those.", MonitorRole, ReaderRole)
	p.ExistingRoles = append(p.ExistingRoles, f.ExistingRoles...)
	if len(f.ExistingRoles) > 0 {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s already exist; their passwords will be reset. Anything else using them must be updated.", strings.Join(f.ExistingRoles, " and ")))
	}
	p.Statements = append(p.Statements, roleStatements(cfg.Database, f.ExistingRoles, "••••••••", "••••••••")...)
	return p, nil
}

// rolesWork reports whether both stored roles can log in to this node.
func (s *service) rolesWork(ctx context.Context, dsn string, managed map[string]repository.ManagedRole) bool {
	for _, purpose := range []string{"monitor", "reader"} {
		d, err := s.dsnFor(dsn, managed[purpose])
		if err != nil {
			return false
		}
		cfg, err := pgx.ParseConfig(d)
		if err != nil {
			return false
		}
		cfg.ConnectTimeout = 5 * time.Second
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return false
		}
		_ = conn.Close(context.Background())
	}
	return true
}

func (s *service) dsnFor(dsn string, m repository.ManagedRole) (string, error) {
	pw, err := s.box.Open(m.PasswordCiphertext, aad(m.SystemIdentifier, m.Purpose))
	if err != nil {
		return "", err
	}
	return WithCredentials(dsn, m.RoleName, string(pw))
}

// roleStatements creates (or resets) the two roles.
func roleStatements(database string, existing []string, monitorPassword, readerPassword string) []string {
	return targetdb.RoleStatements(database, MonitorRole, ReaderRole, existing, monitorPassword, readerPassword)
}

func generatePassword() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ErrNeedsConfirmation means create_roles was not confirmed.
var ErrNeedsConfirmation = errors.New("confirm the statements LockLens will run to create its roles")

// SetupError is a problem the administrator can act on.
type SetupError struct{ Message string }

func (e *SetupError) Error() string { return e.Message }

// Add inspects the connection string again and adds the instance with the
// resulting plan. It returns the new target and the plan that was applied.
func (s *service) Add(ctx context.Context, req Request) (targets.View, Plan, error) {
	p, err := s.Inspect(ctx, req.DSN)
	if err != nil {
		return targets.View{}, p, err
	}
	if !p.OK {
		msg := "cannot connect"
		if p.Error != nil {
			msg = *p.Error
		}
		return targets.View{}, p, &SetupError{Message: msg}
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = p.SuggestedName
	}
	monitorDSN, readerDSN := req.DSN, req.DSN
	switch p.Mode {
	case ModeUnsupported:
		return targets.View{}, p, &SetupError{Message: p.Reason}
	case ModeDirect:
		if !p.WorkspaceAllowed {
			readerDSN = ""
		}
	case ModeReuseRoles:
		if monitorDSN, err = s.dsnFor(req.DSN, p.managed["monitor"]); err != nil {
			return targets.View{}, p, err
		}
		if readerDSN, err = s.dsnFor(req.DSN, p.managed["reader"]); err != nil {
			return targets.View{}, p, err
		}
	case ModeCreateRoles:
		if !req.Confirm {
			return targets.View{}, p, ErrNeedsConfirmation
		}
		if monitorDSN, readerDSN, err = s.createRoles(ctx, req.DSN, p); err != nil {
			return targets.View{}, p, err
		}
	}
	v, err := s.targets.Create(ctx, targets.Input{Name: name, DSN: monitorDSN, Environment: req.Environment,
		Description: "Added from a connection string (" + strings.ReplaceAll(p.Mode, "_", " ") + ")"})
	if err != nil {
		return v, p, err
	}
	if readerDSN != "" && s.workspace != nil {
		pol := repository.WorkspacePolicy{Metadata: true, Data: req.BrowseData, SQL: req.RunSQL}
		if err := s.workspace.Configure(ctx, v.ID, pol, readerDSN); err != nil {
			p.Warnings = append(p.Warnings, "The instance was added, but the Explorer could not be set up: "+workspace.SafeError(err).Message)
		}
	}
	return v, p, nil
}

// createRoles runs the role statements with the administrative connection,
// stores the generated passwords, and checks that the monitoring role can
// log in. The administrative connection is closed afterwards and never kept.
func (s *service) createRoles(ctx context.Context, dsn string, p Plan) (string, string, error) {
	cfg, red, err := connectConfig(dsn)
	if err != nil {
		return "", "", err
	}
	monPW, err := generatePassword()
	if err != nil {
		return "", "", err
	}
	readPW, err := generatePassword()
	if err != nil {
		return "", "", err
	}
	red.Add(monPW)
	red.Add(readPW)
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := pgx.ConnectConfig(cctx, cfg)
	if err != nil {
		return "", "", &SetupError{Message: red.String(err.Error())}
	}
	defer conn.Close(context.Background())
	err = pgx.BeginFunc(cctx, conn, func(tx pgx.Tx) error {
		for _, st := range roleStatements(cfg.Database, p.ExistingRoles, monPW, readPW) {
			if _, err := tx.Exec(cctx, st); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", "", &SetupError{Message: "creating the roles failed: " + red.String(err.Error())}
	}
	for purpose, pair := range map[string][2]string{"monitor": {MonitorRole, monPW}, "reader": {ReaderRole, readPW}} {
		ct, err := s.box.Seal([]byte(pair[1]), aad(p.systemIdentifier, purpose))
		if err != nil {
			return "", "", err
		}
		if err := s.store.PutManagedRole(ctx, repository.ManagedRole{SystemIdentifier: p.systemIdentifier, Purpose: purpose, RoleName: pair[0], PasswordCiphertext: ct}); err != nil {
			return "", "", err
		}
	}
	monitorDSN, err := WithCredentials(dsn, MonitorRole, monPW)
	if err != nil {
		return "", "", err
	}
	readerDSN, err := WithCredentials(dsn, ReaderRole, readPW)
	if err != nil {
		return "", "", err
	}
	check, err := pgx.ParseConfig(monitorDSN)
	if err != nil {
		return "", "", err
	}
	check.ConnectTimeout = 5 * time.Second
	c, err := pgx.ConnectConfig(cctx, check)
	if err != nil {
		return "", "", &SetupError{Message: "the roles were created, but " + MonitorRole + " cannot log in (check pg_hba.conf for this user): " + red.String(err.Error())}
	}
	_ = c.Close(context.Background())
	return monitorDSN, readerDSN, nil
}
