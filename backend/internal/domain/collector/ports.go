package collector

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	changes.Recorder
	InsertActivitySnapshot(ctx context.Context, targetID int64, ts time.Time, blocked, roots, longTx int, snapshot []byte) error
	InsertGap(ctx context.Context, targetID int64, collector string, from, to time.Time, reason string) error
	InsertLoadBucket(ctx context.Context, targetID int64, b observe.LoadBucket) error
	InsertPoints(ctx context.Context, targetID int64, ts time.Time, points []observe.Point) error
	InsertStateSnapshot(ctx context.Context, targetID int64, kind string, ts time.Time, payload []byte) error
	InsertStatements(ctx context.Context, targetID int64, c observe.StatementsCollection, includeText bool, cache repository.IdentityCache) error
	InsertTableHealth(ctx context.Context, targetID int64, h observe.TableHealth) error
	ListTargets(ctx context.Context) ([]repository.Target, error)
	SaveCatalog(ctx context.Context, targetID int64, c observe.Catalog) error
	PruneCatalogs(ctx context.Context, targetID int64, keep []string) error
	SaveTargetStatus(ctx context.Context, st repository.TargetStatus) error
	SaveLiveState(ctx context.Context, targetID int64, serverID string, at time.Time, view []byte) error
	LiveState(ctx context.Context, targetID int64) (serverID string, at time.Time, view []byte, err error)
}

var _ Store = (*repository.Store)(nil)
