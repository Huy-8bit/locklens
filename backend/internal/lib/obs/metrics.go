package obs

// Metrics are the instruments LockLens exports. Label values are bounded:
// collector is one of a fixed set, outcome is ok/error, target is a numeric
// target ID (bounded by the number of configured targets).
type Metrics struct {
	Registry *Registry

	CollectDuration  *Histogram
	CollectTotal     *Counter
	CollectFreshness *Gauge
	ActiveTargets    *Gauge
	StoreWriteErrors *Counter
	StoreDropped     *Counter
	WriteQueueLength *Gauge
	WriteQueueCap    *Gauge
	IncidentsOpened  *Counter
	IncidentsOpen    *Gauge
	HTTPRequests     *Counter
	SSEClients       *Gauge
	CollectSlotsBusy *Gauge

	// Scaling instruments (see docs/test-report.md, "Scaling measurements").
	CollectLag        *Histogram
	CollectLagRatio   *Histogram
	CollectSlotWait   *Histogram
	CollectThrottle   *Counter
	StoreWriteSeconds *Histogram
	StoreQueueWait    *Histogram
	StoreDroppedOps   *Counter
	StatusWrite       *Histogram
	RecorderAnalyze   *Histogram
	PoolConns         *Gauge
	PoolMaxConns      *Gauge
	PoolAcquires      *Counter
	PoolEmptyAcquires *Counter
	PoolAcquireWait   *Counter
	PoolCanceled      *Counter
	ManagerSync       *Histogram
	ClusterAssign     *Histogram
	ClusterAssignErr  *Counter
	LivePublish       *Histogram
	LivePublishBytes  *Counter
	LiveViewBytes     *Histogram
	LivePublishErrors *Counter
	LiveRemoteRead    *Histogram
	BusQueueLength    *Gauge
	BusSent           *Counter
	BusDropped        *Counter
	BusReceived       *Counter
}

var (
	latencyBuckets = []float64{0.0005, 0.001, 0.0025, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}
	ratioBuckets   = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.5, 2, 5, 10}
	sizeBuckets    = []float64{1 << 10, 4 << 10, 16 << 10, 64 << 10, 128 << 10, 256 << 10, 512 << 10, 1 << 20, 2 << 20, 4 << 20}
)

// NewMetrics registers all instruments.
func NewMetrics() *Metrics {
	r := NewRegistry()
	return &Metrics{
		Registry: r,
		CollectDuration: r.NewHistogram("locklens_collect_duration_seconds", "Duration of one collector run.",
			[]float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30}, "collector"),
		CollectTotal:     r.NewCounter("locklens_collect_total", "Collector runs by outcome.", "collector", "outcome"),
		CollectFreshness: r.NewGauge("locklens_collect_last_success_timestamp_seconds", "Unix time of the last successful activity collection per target; freshness is time() minus this value.", "target"),
		ActiveTargets:    r.NewGauge("locklens_active_targets", "Targets with a running collector worker."),
		StoreWriteErrors: r.NewCounter("locklens_store_write_errors_total", "Failed writes to the monitoring store (each retry counts)."),
		StoreDropped:     r.NewCounter("locklens_store_dropped_batches_total", "Collected batches dropped because the write queue was full."),
		WriteQueueLength: r.NewGauge("locklens_write_queue_length", "Batches waiting to be written to the monitoring store."),
		WriteQueueCap:    r.NewGauge("locklens_write_queue_capacity", "Capacity of the write queue."),
		IncidentsOpened:  r.NewCounter("locklens_incidents_opened_total", "Incidents opened by rule.", "rule"),
		IncidentsOpen:    r.NewGauge("locklens_incidents_open", "Currently open incidents."),
		HTTPRequests:     r.NewCounter("locklens_http_requests_total", "HTTP requests by route class and status class.", "route", "status"),
		SSEClients:       r.NewGauge("locklens_sse_clients", "Connected server-sent event clients."),
		CollectSlotsBusy: r.NewGauge("locklens_collect_slots_busy", "Collections currently running (bounded by the global limit)."),

		CollectLag: r.NewHistogram("locklens_collect_lag_seconds",
			"Delay between a collector run's scheduled time and its actual start (includes waiting for a collection slot).", latencyBuckets, "collector"),
		CollectLagRatio: r.NewHistogram("locklens_collect_lag_ratio",
			"Collector lag divided by the interval in effect for that run.", ratioBuckets, "collector"),
		CollectSlotWait: r.NewHistogram("locklens_collect_slot_wait_seconds",
			"Time a collector run waited for a slot of LOCKLENS_MAX_CONCURRENT_COLLECTIONS.", latencyBuckets, "collector"),
		CollectThrottle: r.NewCounter("locklens_collect_throttle_total",
			"Changes of a collector's slow-down factor (direction up: runs less often because it exceeded its time budget).", "collector", "direction"),
		StoreWriteSeconds: r.NewHistogram("locklens_store_write_seconds",
			"Duration of one successful write operation to the monitoring store.", latencyBuckets, "collector"),
		StoreQueueWait: r.NewHistogram("locklens_store_queue_wait_seconds",
			"Time from enqueueing a write operation to its successful write.", latencyBuckets),
		StoreDroppedOps: r.NewCounter("locklens_store_dropped_ops_total",
			"Write operations dropped, by reason.", "reason"),
		StatusWrite: r.NewHistogram("locklens_store_status_write_seconds",
			"Duration of saving a target's collector status (written directly, not through the writer).", latencyBuckets),
		RecorderAnalyze: r.NewHistogram("locklens_recorder_analyze_seconds",
			"Duration of rule evaluation and incident recording after an activity collection.", latencyBuckets),
		PoolConns:         r.NewGauge("locklens_store_pool_conns", "Monitoring store pool connections by state.", "state"),
		PoolMaxConns:      r.NewGauge("locklens_store_pool_max_conns", "Maximum connections of the monitoring store pool."),
		PoolAcquires:      r.NewCounter("locklens_store_pool_acquires_total", "Connections acquired from the monitoring store pool."),
		PoolEmptyAcquires: r.NewCounter("locklens_store_pool_empty_acquires_total", "Acquires that had to wait because no connection was idle."),
		PoolAcquireWait:   r.NewCounter("locklens_store_pool_acquire_wait_seconds_total", "Total time spent acquiring store connections."),
		PoolCanceled:      r.NewCounter("locklens_store_pool_canceled_acquires_total", "Acquires cancelled by their context while waiting."),
		ManagerSync:       r.NewHistogram("locklens_manager_sync_seconds", "Duration of one synchronisation of collector workers with the stored targets.", latencyBuckets),
		ClusterAssign:     r.NewHistogram("locklens_cluster_assign_seconds", "Duration of renewing and taking target leases on one sync.", latencyBuckets),
		ClusterAssignErr:  r.NewCounter("locklens_cluster_assign_errors_total", "Failed target lease assignments (workers are kept, leases not renewed)."),
		LivePublish:       r.NewHistogram("locklens_live_publish_seconds", "Duration of publishing the live views of this server's targets (one tick).", latencyBuckets),
		LivePublishBytes:  r.NewCounter("locklens_live_publish_bytes_total", "Bytes of live views written to live_state."),
		LiveViewBytes:     r.NewHistogram("locklens_live_view_bytes", "Size of one published live view (JSON).", sizeBuckets),
		LivePublishErrors: r.NewCounter("locklens_live_publish_errors_total", "Failed live view writes."),
		LiveRemoteRead:    r.NewHistogram("locklens_live_remote_read_seconds", "Reading a live view published by another server, including waiting for the cache lock.", latencyBuckets),
		BusQueueLength:    r.NewGauge("locklens_bus_queue_length", "Messages waiting to be sent to the other servers."),
		BusSent:           r.NewCounter("locklens_bus_sent_total", "Messages sent to the other servers, by kind.", "kind"),
		BusDropped:        r.NewCounter("locklens_bus_dropped_total", "Messages not sent to the other servers, by reason.", "reason"),
		BusReceived:       r.NewCounter("locklens_bus_received_total", "Messages received from the other servers."),
	}
}
