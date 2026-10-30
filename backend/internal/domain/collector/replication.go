package collector

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// replicationPoints derives time series from a replication collection.
func replicationPoints(r observe.Replication) []observe.Point {
	var pts []observe.Point
	if r.Role == "primary" {
		pts = append(pts, observe.Point{Metric: "replication.replicas", Value: observe.F(float64(len(r.Replicas)))})
		var maxLag *float64
		for _, x := range r.Replicas {
			label := x.ApplicationName
			if label == "" {
				label = "pid " + itoa(x.PID)
			}
			pts = append(pts, observe.Point{Metric: "replication.replay_lag_bytes", Label: label, Value: x.ReplayLagBytes})
			if x.ReplayLagBytes != nil && (maxLag == nil || *x.ReplayLagBytes > *maxLag) {
				maxLag = x.ReplayLagBytes
			}
		}
		if len(r.Replicas) > 0 {
			pts = append(pts, observe.Point{Metric: "replication.max_replay_lag_bytes", Value: maxLag})
		}
	}
	if r.Standby != nil {
		pts = append(pts, observe.Point{Metric: "standby.replay_delay_bytes", Value: r.Standby.ReplayDelayBytes})
	}
	var maxRetained *float64
	for _, s := range r.Slots {
		pts = append(pts, observe.Point{Metric: "slots.retained_bytes", Label: s.Name, Value: s.RetainedBytes})
		if s.RetainedBytes != nil && (maxRetained == nil || *s.RetainedBytes > *maxRetained) {
			maxRetained = s.RetainedBytes
		}
	}
	if len(r.Slots) > 0 {
		pts = append(pts, observe.Point{Metric: "slots.max_retained_bytes", Value: maxRetained})
	}
	return pts
}

func (w *worker) collectReplication(ctx context.Context) error {
	var r observe.Replication
	if err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
		var err error
		r, err = targetdb.ReadReplication(ctx, tx, *w.caps)
		return err
	}); err != nil {
		return err
	}
	pts := replicationPoints(r)
	w.state.mu.Lock()
	w.state.replication = &r
	w.state.mu.Unlock()
	w.state.addPoints(r.At, pts)
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	id, at := w.target.ID, r.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorReplication, At: at,
		Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
			if err := s.InsertStateSnapshot(ctx, id, "replication", at, raw); err != nil {
				return err
			}
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
