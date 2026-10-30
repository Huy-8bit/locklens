package optimizer

import "github.com/locklens/locklens/internal/repository"

// Repositories are the stores this package reads and writes.
type Repositories struct {
	repository.OptimizerRepository
	repository.PlanRepository
	repository.QueryRepository
	repository.TargetRepository
}
