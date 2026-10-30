package httpapi

import "github.com/locklens/locklens/internal/repository"

// Repositories are the stores the handlers read and write.
type Repositories struct {
	repository.AuthRepository
	repository.FindingRepository
	repository.HealthRepository
	repository.IncidentRepository
	repository.LoadRepository
	repository.MetricRollupRepository
	repository.OptimizerRepository
	repository.PlanRepository
	repository.QueryRepository
	repository.RetentionRepository
	repository.SeriesRepository
	repository.SnapshotRepository
	repository.TargetRepository
	repository.TimescaleRepository
	repository.TopologyRepository
	repository.WorkspaceRepository
}
