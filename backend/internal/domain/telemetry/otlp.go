package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// Pusher sends the export to an OpenTelemetry collector over OTLP/HTTP
// (JSON encoding) at an interval: gauges as gauges, counters as cumulative
// monotonic sums starting when this server started.
type Pusher struct {
	Service  Service
	Endpoint string
	Headers  map[string]string
	Interval time.Duration
	// ServerID is the resource's service.instance.id.
	ServerID string
	Version  string
	Client   *http.Client
	Log      *slog.Logger
	start    time.Time
}

// Run pushes until ctx ends.
func (p *Pusher) Run(ctx context.Context) {
	p.start = time.Now()
	if p.Client == nil {
		p.Client = &http.Client{Timeout: 30 * time.Second}
	}
	t := time.NewTicker(p.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := p.push(ctx); err != nil && ctx.Err() == nil {
			p.Log.Warn("OTLP push failed", "error", err.Error())
		}
	}
}

func (p *Pusher) push(ctx context.Context) error {
	fams, err := p.Service.Collect(ctx, 0)
	if err != nil {
		return err
	}
	body, err := json.Marshal(otlpRequest(fams, p.ServerID, p.Version, p.start, time.Now()))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range p.Headers {
		req.Header.Set(k, v)
	}
	resp, err := p.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("collector answered HTTP %d", resp.StatusCode)
	}
	return nil
}

type otlpAttr struct {
	Key   string            `json:"key"`
	Value map[string]string `json:"value"`
}

func attrs(ls []Label) []otlpAttr {
	out := make([]otlpAttr, len(ls))
	for i, l := range ls {
		out[i] = otlpAttr{Key: l.Name, Value: map[string]string{"stringValue": l.Value}}
	}
	return out
}

// otlpRequest is an ExportMetricsServiceRequest in OTLP's JSON encoding.
func otlpRequest(fams []Family, serverID, version string, start, now time.Time) map[string]any {
	ts := strconv.FormatInt(now.UnixNano(), 10)
	st := strconv.FormatInt(start.UnixNano(), 10)
	metrics := make([]map[string]any, 0, len(fams))
	for _, f := range fams {
		points := make([]map[string]any, 0, len(f.Samples))
		for _, s := range f.Samples {
			pt := map[string]any{"attributes": attrs(s.Labels), "timeUnixNano": ts, "asDouble": s.Value}
			if f.Type == Counter {
				pt["startTimeUnixNano"] = st
			}
			points = append(points, pt)
		}
		m := map[string]any{"name": f.Name, "description": f.Help}
		if f.Type == Counter {
			m["sum"] = map[string]any{"dataPoints": points, "aggregationTemporality": 2, "isMonotonic": true}
		} else {
			m["gauge"] = map[string]any{"dataPoints": points}
		}
		metrics = append(metrics, m)
	}
	return map[string]any{"resourceMetrics": []any{map[string]any{
		"resource": map[string]any{"attributes": attrs([]Label{{"service.name", "locklens"}, {"service.instance.id", serverID},
			{"service.version", version}})},
		"scopeMetrics": []any{map[string]any{"scope": map[string]any{"name": "locklens"}, "metrics": metrics}},
	}}}
}
