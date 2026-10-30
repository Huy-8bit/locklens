package incidents

import "github.com/locklens/locklens/internal/repository"

// Repositories are the stores this package reads and writes.
type Repositories struct {
	repository.FindingRepository
	repository.IncidentRepository
}
