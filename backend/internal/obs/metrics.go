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
}

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
	}
}
