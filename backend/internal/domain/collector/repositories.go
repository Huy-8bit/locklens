package collector

import "github.com/locklens/locklens/internal/repository"

// Repositories are the stores this package reads and writes.
type Repositories struct {
	repository.ChangeRepository
	repository.ClusterRepository
	repository.CollectionRepository
	repository.LoadRepository
	repository.OptimizerRepository
	repository.TargetRepository
}
