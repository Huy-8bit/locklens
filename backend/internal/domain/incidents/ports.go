package incidents

import (
	"context"
	"time"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

// Store is the part of the monitoring store this package uses. It is
// implemented by *repository.Store; tests can substitute a fake.
type Store interface {
	AppendIncidentEvents(ctx context.Context, id int64, lastSeen *time.Time, events []repository.IncidentEvent) error
	CreateIncident(ctx context.Context, in repository.Incident, events []repository.IncidentEvent) (int64, error)
	LatestResolvedIncident(ctx context.Context, targetID int64, ruleID string, since time.Time) (repository.Incident, error)
	LinkFindingsToIncident(ctx context.Context, incidentID int64, findingIDs []int64) error
	OpenIncidents(ctx context.Context) ([]repository.Incident, error)
	SyncFindings(ctx context.Context, targetID int64, now time.Time, fs []rules.Finding) (map[[2]string]int64, error)
	UpdateIncidentState(ctx context.Context, id int64, state string, resolvedAt *time.Time, resolution *string, severity, title string, truncated bool) error
}

var _ Store = (*repository.Store)(nil)
