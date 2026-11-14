package repository

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// OutboxRepository is the notification outbox: incident changes to notify
// about, written with the change and sent by the server running the
// notification job.
type OutboxRepository interface {
	// EnqueueNotification records an incident change to notify about (in the
	// transaction of the change when called under Fenced).
	EnqueueNotification(ctx context.Context, incidentID int64, event string) error
	// ClaimNotifications claims for holder the rows of up to limit
	// incidents, oldest first: rows nobody claimed, holder claimed, or whose
	// claim is older than stale. All rows of an incident are claimed
	// together, and an incident another sender is processing is skipped.
	ClaimNotifications(ctx context.Context, holder string, limit int, stale time.Duration) ([]OutboxItem, error)
	// DoneNotifications removes processed rows still claimed by holder.
	DoneNotifications(ctx context.Context, ids []int64, holder string) error
	// PendingNotifications returns the number of rows and the oldest one's
	// time (nil when empty).
	PendingNotifications(ctx context.Context) (int, *time.Time, error)
}

var _ OutboxRepository = (*Store)(nil)

// OutboxItem is an incident change to notify about.
type OutboxItem struct {
	ID         int64
	IncidentID int64
	Event      string
	CreatedAt  time.Time
}

// EnqueueNotification records an incident change to notify about.
func (s *Store) EnqueueNotification(ctx context.Context, incidentID int64, event string) error {
	_, err := s.db.Exec(ctx, queries.EnqueueNotification, incidentID, event)
	return s.red.Error(err)
}

// ClaimNotifications claims outbox rows; see OutboxRepository.
func (s *Store) ClaimNotifications(ctx context.Context, holder string, limit int, stale time.Duration) ([]OutboxItem, error) {
	rows, err := s.db.Query(ctx, queries.ClaimNotifications, holder, limit, stale.Seconds())
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (OutboxItem, error) {
		var x OutboxItem
		err := r.Scan(&x.ID, &x.IncidentID, &x.Event, &x.CreatedAt)
		return x, err
	})
	return out, s.red.Error(err)
}

// DoneNotifications removes processed rows still claimed by holder.
func (s *Store) DoneNotifications(ctx context.Context, ids []int64, holder string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, queries.DoneNotifications, ids, holder)
	return s.red.Error(err)
}

// PendingNotifications returns the outbox size and its oldest row's time.
func (s *Store) PendingNotifications(ctx context.Context) (int, *time.Time, error) {
	var n int
	var oldest *time.Time
	err := s.db.QueryRow(ctx, queries.PendingNotifications).Scan(&n, &oldest)
	return n, oldest, s.red.Error(err)
}
