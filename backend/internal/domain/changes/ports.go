package changes

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Recorder is the part of the monitoring store change detection uses (the
// collectors pass theirs).
type Recorder interface {
	ChangeState(ctx context.Context, targetID int64, kind string) ([]byte, error)
	SaveChangeState(ctx context.Context, targetID int64, kind string, at time.Time, state []byte) error
	InsertChanges(ctx context.Context, events []Event) error
}

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	Recorder
	ListChanges(ctx context.Context, f repository.ChangeFilter) ([]Event, error)
	DeleteChange(ctx context.Context, id int64) (Event, error)
}

var _ Store = (*repository.Store)(nil)
