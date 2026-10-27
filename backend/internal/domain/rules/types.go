// Package rules is LockLens' deterministic, evidence-based rule engine.
//
// Every rule has a stable ID and a version that changes whenever its logic
// changes. A finding states what was observed (facts with evidence), what it
// might mean (possible explanations, never a confirmed root cause), what to
// check next, and the limits of the inference. Missing data never makes a
// rule pass: it produces an insufficient_data finding instead.
package rules

import (
	"errors"
	"fmt"

	"github.com/locklens/locklens/internal/domain/finding"
)

// Aliases of the finding types, which live in package finding so that the
// store can persist them without importing the rule engine.
type (
	Severity = finding.Severity
	Status   = finding.Status
	Evidence = finding.Evidence
	Finding  = finding.Finding
)

// Severities and statuses.
const (
	Info             = finding.Info
	Warning          = finding.Warning
	Critical         = finding.Critical
	Active           = finding.Active
	InsufficientData = finding.InsufficientData
	Resolved         = finding.Resolved
)

// Thresholds configure the rules. All durations are in seconds so the JSON
// form is easy to edit.
type Thresholds struct {
	BlockingMinSeconds      float64 `json:"blocking_min_seconds"`
	BlockingMinSamples      int     `json:"blocking_min_samples"`
	BlockingMinBlocked      int     `json:"blocking_min_blocked"`
	BlockingCriticalBlocked int     `json:"blocking_critical_blocked"`
	BlockingCriticalSeconds float64 `json:"blocking_critical_seconds"`

	IdleBlockerMinSamples int `json:"idle_blocker_min_samples"`

	ConnectionsWarnRatio     float64 `json:"connections_warn_ratio"`
	ConnectionsCriticalRatio float64 `json:"connections_critical_ratio"`
	ConnectionsMinSamples    int     `json:"connections_min_samples"`

	LongTxWarnSeconds     float64 `json:"long_transaction_warn_seconds"`
	LongTxCriticalSeconds float64 `json:"long_transaction_critical_seconds"`

	WorkloadWindowSeconds float64 `json:"workload_window_seconds"`
	WorkloadMinCoverage   float64 `json:"workload_min_coverage"`
	RegressionMinCalls    int64   `json:"regression_min_calls"`
	RegressionMeanRatio   float64 `json:"regression_mean_ratio"`
	RegressionMinDeltaMS  float64 `json:"regression_min_delta_ms"`
	VolumeCallsRatio      float64 `json:"volume_calls_ratio"`
	VolumeMinCalls        int64   `json:"volume_min_calls"`

	TempWindowSeconds float64 `json:"temp_window_seconds"`
	TempRatio         float64 `json:"temp_ratio"`
	TempMinBytes      float64 `json:"temp_min_bytes"`

	DeadlockWindowSeconds float64 `json:"deadlock_window_seconds"`

	DeadTuplesMin              int64   `json:"dead_tuples_min"`
	DeadTuplesRatio            float64 `json:"dead_tuples_ratio"`
	DeadTuplesVacuumAgeSeconds float64 `json:"dead_tuples_vacuum_age_seconds"`
	XIDAgeWarnFraction         float64 `json:"xid_age_warn_fraction"`
	XIDAgeCriticalFraction     float64 `json:"xid_age_critical_fraction"`

	ReplicaLagBytes       float64 `json:"replica_lag_bytes"`
	ReplicaLagMinSamples  int     `json:"replica_lag_min_samples"`
	SlotRetainedBytes     float64 `json:"slot_retained_bytes"`
	ArchiverWindowSeconds float64 `json:"archiver_window_seconds"`

	// Running statements (queries.long_running): absolute limits, and a
	// limit relative to the statement's usual time per call.
	SlowQueryWarnSeconds        float64 `json:"slow_query_warn_seconds"`
	SlowQueryCriticalSeconds    float64 `json:"slow_query_critical_seconds"`
	SlowQueryExpectedFactor     float64 `json:"slow_query_expected_factor"`
	SlowQueryExpectedMinSeconds float64 `json:"slow_query_expected_min_seconds"`

	// Query baselines: history used, size of a point, points needed before
	// a baseline is trusted (warm-up), and calls a point needs for per-call
	// measures.
	BaselineDays             float64 `json:"baseline_days"`
	BaselinePointSeconds     float64 `json:"baseline_point_seconds"`
	BaselineWarmupPoints     int     `json:"baseline_warmup_points"`
	BaselineMinCallsPerPoint int64   `json:"baseline_min_calls_per_point"`
	// AnomalySensitivity is the robust z-score (deviations from the median,
	// estimated with the MAD) a value must reach to be unusual.
	AnomalySensitivity float64 `json:"anomaly_sensitivity"`

	// Statement anomalies compared with the baseline.
	RegressionCriticalRatio float64 `json:"regression_critical_ratio"`
	LoadRatio               float64 `json:"load_ratio"`
	LoadMinAddedMSPerSec    float64 `json:"load_min_added_ms_per_s"`
	IORatio                 float64 `json:"io_ratio"`
	IOMinReadsPerCall       float64 `json:"io_min_reads_per_call"`
	PatternRowsRatio        float64 `json:"pattern_rows_ratio"`
	NPlusOneMinCallsPerSec  float64 `json:"n_plus_one_min_calls_per_s"`
	NPlusOneMaxMeanMS       float64 `json:"n_plus_one_max_mean_ms"`
	NPlusOneMaxRowsPerCall  float64 `json:"n_plus_one_max_rows_per_call"`
}

// Defaults returns the default thresholds.
func Defaults() Thresholds {
	return Thresholds{
		BlockingMinSeconds: 30, BlockingMinSamples: 3, BlockingMinBlocked: 1,
		BlockingCriticalBlocked: 10, BlockingCriticalSeconds: 300,
		IdleBlockerMinSamples: 2,
		ConnectionsWarnRatio:  0.8, ConnectionsCriticalRatio: 0.95, ConnectionsMinSamples: 2,
		LongTxWarnSeconds: 300, LongTxCriticalSeconds: 1800,
		WorkloadWindowSeconds: 900, WorkloadMinCoverage: 0.8,
		RegressionMinCalls: 50, RegressionMeanRatio: 2, RegressionMinDeltaMS: 5,
		VolumeCallsRatio: 3, VolumeMinCalls: 500,
		TempWindowSeconds: 900, TempRatio: 3, TempMinBytes: 64 << 20,
		DeadlockWindowSeconds: 900,
		DeadTuplesMin:         10000, DeadTuplesRatio: 0.2, DeadTuplesVacuumAgeSeconds: 86400,
		XIDAgeWarnFraction: 0.5, XIDAgeCriticalFraction: 0.9,
		ReplicaLagBytes: 64 << 20, ReplicaLagMinSamples: 2,
		SlotRetainedBytes:     1 << 30,
		ArchiverWindowSeconds: 900,
		SlowQueryWarnSeconds:  30, SlowQueryCriticalSeconds: 300, SlowQueryExpectedFactor: 20, SlowQueryExpectedMinSeconds: 5,
		BaselineDays: 7, BaselinePointSeconds: 3600, BaselineWarmupPoints: 24, BaselineMinCallsPerPoint: 20,
		AnomalySensitivity:      4,
		RegressionCriticalRatio: 5,
		LoadRatio:               3, LoadMinAddedMSPerSec: 50,
		IORatio: 3, IOMinReadsPerCall: 100,
		PatternRowsRatio:       5,
		NPlusOneMinCallsPerSec: 50, NPlusOneMaxMeanMS: 1, NPlusOneMaxRowsPerCall: 1,
	}
}

// DemoDefaults returns shorter windows and lower limits for the local lab
// (LOCKLENS_DEMO_MODE), so findings appear within minutes instead of the
// production-oriented tens of minutes. They are not recommended for real use.
func DemoDefaults() Thresholds {
	t := Defaults()
	t.BlockingMinSeconds = 20
	t.LongTxWarnSeconds = 60
	t.LongTxCriticalSeconds = 600
	t.WorkloadWindowSeconds = 120
	t.RegressionMinCalls = 30
	t.VolumeMinCalls = 200
	t.TempWindowSeconds = 120
	t.TempMinBytes = 16 << 20
	t.DeadlockWindowSeconds = 300
	t.ReplicaLagBytes = 16 << 20
	t.SlotRetainedBytes = 32 << 20
	t.ArchiverWindowSeconds = 300
	t.SlowQueryWarnSeconds = 10
	t.SlowQueryCriticalSeconds = 60
	t.BaselinePointSeconds = 300
	t.BaselineWarmupPoints = 6
	t.BaselineMinCallsPerPoint = 5
	t.LoadMinAddedMSPerSec = 5
	t.IOMinReadsPerCall = 10
	t.NPlusOneMinCallsPerSec = 5
	return t
}

// Validate checks that thresholds are usable.
func (t Thresholds) Validate() error {
	var errs []error
	pos := func(name string, v float64) {
		if !(v > 0) {
			errs = append(errs, fmt.Errorf("%s must be greater than 0", name))
		}
	}
	ratio := func(name string, v float64) {
		if !(v > 0 && v <= 1) {
			errs = append(errs, fmt.Errorf("%s must be in (0, 1]", name))
		}
	}
	atLeast := func(name string, v, lo int64) {
		if v < lo {
			errs = append(errs, fmt.Errorf("%s must be at least %d", name, lo))
		}
	}
	pos("blocking_min_seconds", t.BlockingMinSeconds)
	atLeast("blocking_min_samples", int64(t.BlockingMinSamples), 1)
	atLeast("blocking_min_blocked", int64(t.BlockingMinBlocked), 1)
	atLeast("blocking_critical_blocked", int64(t.BlockingCriticalBlocked), int64(t.BlockingMinBlocked))
	pos("blocking_critical_seconds", t.BlockingCriticalSeconds)
	atLeast("idle_blocker_min_samples", int64(t.IdleBlockerMinSamples), 1)
	ratio("connections_warn_ratio", t.ConnectionsWarnRatio)
	ratio("connections_critical_ratio", t.ConnectionsCriticalRatio)
	if t.ConnectionsCriticalRatio < t.ConnectionsWarnRatio {
		errs = append(errs, errors.New("connections_critical_ratio must be at least connections_warn_ratio"))
	}
	atLeast("connections_min_samples", int64(t.ConnectionsMinSamples), 1)
	pos("long_transaction_warn_seconds", t.LongTxWarnSeconds)
	if t.LongTxCriticalSeconds < t.LongTxWarnSeconds {
		errs = append(errs, errors.New("long_transaction_critical_seconds must be at least long_transaction_warn_seconds"))
	}
	if t.WorkloadWindowSeconds < 60 || t.WorkloadWindowSeconds > 86400 {
		errs = append(errs, errors.New("workload_window_seconds must be between 60 and 86400"))
	}
	ratio("workload_min_coverage", t.WorkloadMinCoverage)
	atLeast("regression_min_calls", t.RegressionMinCalls, 1)
	if t.RegressionMeanRatio <= 1 {
		errs = append(errs, errors.New("regression_mean_ratio must be greater than 1"))
	}
	if t.RegressionMinDeltaMS < 0 {
		errs = append(errs, errors.New("regression_min_delta_ms must not be negative"))
	}
	if t.VolumeCallsRatio <= 1 {
		errs = append(errs, errors.New("volume_calls_ratio must be greater than 1"))
	}
	atLeast("volume_min_calls", t.VolumeMinCalls, 1)
	if t.TempWindowSeconds < 60 {
		errs = append(errs, errors.New("temp_window_seconds must be at least 60"))
	}
	if t.TempRatio <= 1 {
		errs = append(errs, errors.New("temp_ratio must be greater than 1"))
	}
	pos("temp_min_bytes", t.TempMinBytes)
	if t.DeadlockWindowSeconds < 60 {
		errs = append(errs, errors.New("deadlock_window_seconds must be at least 60"))
	}
	atLeast("dead_tuples_min", t.DeadTuplesMin, 1)
	ratio("dead_tuples_ratio", t.DeadTuplesRatio)
	pos("dead_tuples_vacuum_age_seconds", t.DeadTuplesVacuumAgeSeconds)
	ratio("xid_age_warn_fraction", t.XIDAgeWarnFraction)
	ratio("xid_age_critical_fraction", t.XIDAgeCriticalFraction)
	pos("replica_lag_bytes", t.ReplicaLagBytes)
	atLeast("replica_lag_min_samples", int64(t.ReplicaLagMinSamples), 1)
	pos("slot_retained_bytes", t.SlotRetainedBytes)
	if t.ArchiverWindowSeconds < 60 {
		errs = append(errs, errors.New("archiver_window_seconds must be at least 60"))
	}
	pos("slow_query_warn_seconds", t.SlowQueryWarnSeconds)
	if t.SlowQueryCriticalSeconds < t.SlowQueryWarnSeconds {
		errs = append(errs, errors.New("slow_query_critical_seconds must be at least slow_query_warn_seconds"))
	}
	if t.SlowQueryExpectedFactor < 2 {
		errs = append(errs, errors.New("slow_query_expected_factor must be at least 2"))
	}
	pos("slow_query_expected_min_seconds", t.SlowQueryExpectedMinSeconds)
	if t.BaselineDays < 1 || t.BaselineDays > 30 {
		errs = append(errs, errors.New("baseline_days must be between 1 and 30"))
	}
	if t.BaselinePointSeconds < 300 || t.BaselinePointSeconds > 86400 || int64(t.BaselinePointSeconds)%300 != 0 {
		errs = append(errs, errors.New("baseline_point_seconds must be a multiple of 300 between 300 and 86400"))
	}
	atLeast("baseline_warmup_points", int64(t.BaselineWarmupPoints), 3)
	atLeast("baseline_min_calls_per_point", t.BaselineMinCallsPerPoint, 1)
	if t.AnomalySensitivity < 2 {
		errs = append(errs, errors.New("anomaly_sensitivity must be at least 2"))
	}
	for name, v := range map[string]float64{"regression_critical_ratio": t.RegressionCriticalRatio, "load_ratio": t.LoadRatio,
		"io_ratio": t.IORatio, "pattern_rows_ratio": t.PatternRowsRatio} {
		if v <= 1 {
			errs = append(errs, fmt.Errorf("%s must be greater than 1", name))
		}
	}
	pos("load_min_added_ms_per_s", t.LoadMinAddedMSPerSec)
	pos("io_min_reads_per_call", t.IOMinReadsPerCall)
	pos("n_plus_one_min_calls_per_s", t.NPlusOneMinCallsPerSec)
	pos("n_plus_one_max_mean_ms", t.NPlusOneMaxMeanMS)
	pos("n_plus_one_max_rows_per_call", t.NPlusOneMaxRowsPerCall)
	return errors.Join(errs...)
}

// Rule IDs and versions. Bump a version when the rule's logic changes.
const (
	RuleBlockingChain    = "blocking.long_chain"
	RuleIdleBlocker      = "blocking.idle_in_tx_blocker"
	RuleConnections      = "connections.pressure"
	RuleLongTransaction  = "transactions.long"
	RuleMeanRegression   = "queries.mean_regression"
	RuleCallVolume       = "queries.call_volume_increase"
	RuleTempFiles        = "temp_files.increase"
	RuleDeadlocks        = "deadlocks.increase"
	RuleDeadTuples       = "vacuum.dead_tuples"
	RuleXIDAge           = "vacuum.xid_age"
	RuleReplicaLag       = "replication.replica_lag"
	RuleSlotRetainedWAL  = "replication.slot_retained_wal"
	RuleArchiverFailures = "wal.archiver_failures"
	RuleLongRunning      = "queries.long_running"
	RuleLoadIncrease     = "queries.load_increase"
	RuleIORegression     = "queries.io_regression"
	RulePatternChange    = "queries.pattern_change"
	RuleNPlusOne         = "queries.possible_n_plus_one"
)

// Versions of the rules above.
var Versions = map[string]int{
	RuleBlockingChain: 1, RuleIdleBlocker: 1, RuleConnections: 1, RuleLongTransaction: 1,
	// Version 2: compared with the statement's own baseline when it has one.
	RuleMeanRegression: 2, RuleCallVolume: 2, RuleTempFiles: 1, RuleDeadlocks: 1,
	RuleDeadTuples: 1, RuleXIDAge: 1, RuleReplicaLag: 1, RuleSlotRetainedWAL: 1, RuleArchiverFailures: 1,
	RuleLongRunning: 1, RuleLoadIncrease: 1, RuleIORegression: 1, RulePatternChange: 1, RuleNPlusOne: 1,
}

// StatementRules report on one statement (their key names its identity).
var StatementRules = []string{RuleMeanRegression, RuleCallVolume, RuleLoadIncrease, RuleIORegression, RulePatternChange, RuleNPlusOne}

// TriggerRules may open incidents.
var TriggerRules = []string{RuleBlockingChain, RuleIdleBlocker, RuleConnections, RuleMeanRegression, RuleSlotRetainedWAL, RuleLongRunning}
