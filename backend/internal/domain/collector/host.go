package collector

import (
	"context"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/lib/hostmetrics"
	"github.com/locklens/locklens/internal/repository"
)

func (w *worker) collectHost(ctx context.Context) error {
	scrape, err := hostmetrics.Fetch(ctx, w.httpClient, *w.target.NodeExporterURL)
	if err != nil {
		msg := sanitizeError(err)
		w.state.mu.Lock()
		w.state.hostError = &msg
		w.state.mu.Unlock()
		return err
	}
	var interval *float64
	if w.prevHost != nil && scrape.At.After(w.prevHost.At) {
		interval = observe.F(scrape.At.Sub(w.prevHost.At).Seconds())
	}
	d := hostmetrics.Derive(w.prevHost, scrape)
	w.prevHost = &scrape
	hm := observe.HostMetrics{At: scrape.At, CPUUtilization: d.CPUUtilization, MemTotalBytes: d.MemTotal,
		MemAvailBytes: d.MemAvailable, DiskReadBps: d.DiskReadBps, DiskWriteBps: d.DiskWriteBps, NetRxBps: d.NetRxBps, NetTxBps: d.NetTxBps}
	pts := []observe.Point{
		{Metric: "host.cpu_utilization_ratio", Value: d.CPUUtilization},
		{Metric: "host.memory_available_bytes", Value: d.MemAvailable},
		{Metric: "host.memory_total_bytes", Value: d.MemTotal},
		{Metric: "host.disk_read_bytes.per_s", IntervalSeconds: interval, Value: d.DiskReadBps},
		{Metric: "host.disk_write_bytes.per_s", IntervalSeconds: interval, Value: d.DiskWriteBps},
		{Metric: "host.network_receive_bytes.per_s", IntervalSeconds: interval, Value: d.NetRxBps},
		{Metric: "host.network_transmit_bytes.per_s", IntervalSeconds: interval, Value: d.NetTxBps},
	}
	for _, fs := range d.Filesystems {
		hm.Filesystems = append(hm.Filesystems, observe.Filesystem{Mountpoint: fs.Mountpoint, FSType: fs.FSType, SizeBytes: fs.Size, AvailBytes: fs.Avail})
		pts = append(pts, observe.Point{Metric: "host.filesystem_used_ratio", Label: fs.Mountpoint, Value: observe.F(1 - fs.Avail/fs.Size)})
	}
	w.state.mu.Lock()
	w.state.host, w.state.hostError = &hm, nil
	w.state.mu.Unlock()
	w.state.addPoints(hm.At, pts)
	id, at := w.target.ID, hm.At
	w.m.writer.Enqueue(WriteOp{TargetID: id, Collector: CollectorHost, At: at,
		Write: func(ctx context.Context, s Store, _ repository.IdentityCache) error {
			return s.InsertPoints(ctx, id, at, pts)
		}})
	return nil
}
