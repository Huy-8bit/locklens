package repository

import "testing"

func TestMetricSemantics(t *testing.T) {
	for _, tc := range []struct{ metric, agg, collector string }{
		{"xact.commit.per_s", "time_weighted_mean", "database"},
		{"deadlocks.delta", "sum", "database"},
		{"sessions.active", "max", "activity"},
		{"transactions.oldest_age_seconds", "max", "activity"},
		{"db.total_size_bytes", "last", "tables"},
		{"cache.hit_ratio", "mean_interval_ratio", "database"},
		{"statements.exec_ms.per_s", "time_weighted_mean", "statements"},
		{"host.disk_read_bytes.per_s", "time_weighted_mean", "host"},
		{"replication.max_replay_lag_bytes", "max", "replication"},
	} {
		if MetricAggregation(tc.metric) != tc.agg || MetricCollector(tc.metric) != tc.collector {
			t.Errorf("wrong semantics for %s", tc.metric)
		}
	}
}
