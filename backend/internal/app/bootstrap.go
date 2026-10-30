package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/domain/targets"
	"github.com/locklens/locklens/internal/domain/workspace"
	"github.com/locklens/locklens/internal/repository"
)

func bootstrapAdmin(ctx context.Context, store repository.AuthRepository, cfg config.Config, log *slog.Logger) error {
	hash := cfg.AdminPasswordHash
	if hash != "" && !auth.IsBcryptHash(hash) {
		return errors.New("LOCKLENS_ADMIN_PASSWORD_HASH is not a bcrypt hash (create one with: locklens admin hash-password)")
	}
	existing, existingErr := store.UserByUsername(ctx, cfg.AdminUser)
	if cfg.AdminPassword != "" {
		if existingErr == nil && auth.VerifyPassword(existing.PasswordHash, cfg.AdminPassword) {
			hash = existing.PasswordHash
		} else {
			var err error
			if hash, err = auth.HashPassword(cfg.AdminPassword); err != nil {
				return fmt.Errorf("LOCKLENS_ADMIN_PASSWORD: %w", err)
			}
		}
	}
	if hash == "" {
		if existingErr == nil && existing.Managed {
			return nil
		}
		return errors.New("no administrator configured: set LOCKLENS_ADMIN_PASSWORD (or LOCKLENS_ADMIN_PASSWORD_HASH) for the first start")
	}
	id, changed, err := store.UpsertManagedAdmin(ctx, cfg.AdminUser, hash)
	if err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	}
	if changed {
		// A changed password ends that administrator's sessions.
		if err := store.DeleteUserSessions(ctx, id); err != nil {
			return err
		}
		log.Info("admin password changed; the administrator's sessions were ended")
	}
	return nil
}

// bootstrapStore holds what bootstrapping targets reads.
type bootstrapStore struct {
	repository.TargetRepository
	repository.WorkspaceRepository
}

func bootstrapTargets(ctx context.Context, svc targets.Service, ws workspace.Service, store bootstrapStore, path string, log *slog.Logger) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("LOCKLENS_BOOTSTRAP_TARGETS_FILE: %w", err)
	}
	defer f.Close()
	var entries []targets.BootstrapEntry
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&entries); err != nil {
		return fmt.Errorf("LOCKLENS_BOOTSTRAP_TARGETS_FILE: invalid JSON: %w", err)
	}
	n, err := svc.Bootstrap(ctx, entries, config.Getenv)
	if err != nil {
		return err
	}
	if n > 0 {
		log.Info("bootstrap targets created", "count", n)
	}
	for _, e := range entries {
		if e.Workspace != nil {
			bootstrapWorkspace(ctx, ws, store, e, log)
		}
	}
	return nil
}

// bootstrapWorkspace configures investigation access for a bootstrap target
// that has none yet. Failures are logged, not fatal: monitoring works
// without it, and the administrator can configure it in the UI.
func bootstrapWorkspace(ctx context.Context, ws workspace.Service, store bootstrapStore, e targets.BootstrapEntry, log *slog.Logger) {
	t, err := store.GetTargetByName(ctx, e.Name)
	if err != nil {
		log.Warn("bootstrap workspace: target not found", "target", e.Name)
		return
	}
	p, err := store.WorkspacePolicy(ctx, t.ID)
	if err != nil || p.Configured {
		return
	}
	dsn := config.Getenv(e.Workspace.DSNEnv)
	if dsn == "" {
		log.Warn("bootstrap workspace: no DSN", "target", e.Name, "env", e.Workspace.DSNEnv)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := ws.Configure(cctx, t.ID, repository.WorkspacePolicy{Metadata: e.Workspace.Metadata, Data: e.Workspace.Data, SQL: e.Workspace.SQL}, dsn); err != nil {
		log.Warn("bootstrap workspace: not configured", "target", e.Name, "error", workspace.SafeError(err).Message)
		return
	}
	log.Info("bootstrap workspace configured", "target", e.Name, "metadata", e.Workspace.Metadata, "data", e.Workspace.Data, "sql", e.Workspace.SQL)
}
