package notify

import (
	"context"

	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	ActiveSilence(ctx context.Context, targetID *int64, ruleID string) (*repository.Silence, error)
	CreateSilence(ctx context.Context, x repository.Silence) (repository.Silence, error)
	DeleteNotificationChannel(ctx context.Context, id int64) error
	EndSilence(ctx context.Context, id int64) error
	GetIncident(ctx context.Context, id int64) (repository.Incident, error)
	GetNotificationChannel(ctx context.Context, id int64) (repository.NotificationChannel, error)
	GetTarget(ctx context.Context, id int64) (repository.Target, error)
	InsertDelivery(ctx context.Context, d repository.Delivery) error
	ListDeliveries(ctx context.Context, incidentID *int64, limit int) ([]repository.Delivery, error)
	ListFindings(ctx context.Context, f repository.FindingFilter) ([]repository.FindingRow, error)
	ListNotificationChannels(ctx context.Context) ([]repository.NotificationChannel, error)
	ListSilences(ctx context.Context, includeEnded bool, limit int) ([]repository.Silence, error)
	OpenUnacknowledgedIncidents(ctx context.Context) ([]repository.Incident, error)
	SaveNotificationChannel(ctx context.Context, c repository.NotificationChannel) (repository.NotificationChannel, error)
}

var _ Store = (*repository.Store)(nil)
