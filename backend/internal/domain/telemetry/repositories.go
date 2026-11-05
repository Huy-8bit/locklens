package telemetry

import "github.com/locklens/locklens/internal/repository"

// Repositories are the stores this package reads.
type Repositories struct {
	repository.FindingRepository
	repository.IncidentRepository
	repository.TargetRepository
}
