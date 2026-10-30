package incidents

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/locklens/locklens/internal/domain/live"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

func latestMetrics(v live.View) map[string]*float64 {
	keys := []string{"connections.used", "connections.usage_ratio", "sessions.active", "sessions.idle_in_transaction",
		"sessions.waiting_lock", "blocking.blocked_sessions", "blocking.max_lock_wait_seconds", "transactions.oldest_age_seconds",
		"xact.commit.per_s", "xact.rollback.per_s", "temp.bytes.per_s", "deadlocks.delta", "wal.bytes.per_s",
		"statements.calls.per_s", "statements.exec_ms.per_s", "slots.max_retained_bytes", "replication.max_replay_lag_bytes",
		"host.cpu_utilization_ratio", "host.memory_available_bytes"}
	out := map[string]*float64{}
	for _, k := range keys {
		if tv, ok := v.Latest(k); ok {
			out[k] = tv.Value
		}
	}
	return out
}

// graphSignature identifies a blocking graph shape so unchanged graphs are
// not stored again.
func graphSignature(s rules.ActivitySample) string {
	h := sha256.New()
	for _, root := range s.Roots {
		fmt.Fprintf(h, "%s|%d|%v|%s;", root.Key, root.Transitive, root.Members, deref(root.State))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func wrapSnapshot(raw json.RawMessage) json.RawMessage {
	out, _ := json.Marshal(map[string]json.RawMessage{"snapshot": raw})
	return out
}

func workloadEvent(v live.View, th rules.Thresholds, now time.Time) *repository.IncidentEvent {
	if len(v.Statements) == 0 {
		return nil
	}
	win := time.Duration(th.WorkloadWindowSeconds * float64(time.Second))
	top, covered := rules.TopQueries(v.Statements, now.Add(-win), now, 10)
	prevTop, prevCovered := rules.TopQueries(v.Statements, now.Add(-2*win), now.Add(-win), 50)
	payload, _ := json.Marshal(map[string]any{"window_seconds": win.Seconds(), "current": top, "current_covered_seconds": covered,
		"previous": prevTop, "previous_covered_seconds": prevCovered})
	return &repository.IncidentEvent{At: now, Kind: KindWorkload, Payload: payload,
		Summary: fmt.Sprintf("Top statements by total execution time in the last %s (from pg_stat_statements deltas).", win)}
}

func summaryLine(s rules.ActivitySample) string {
	ratio := "unknown"
	if r := s.UsageRatio(); r != nil {
		ratio = fmt.Sprintf("%.0f%%", *r*100)
	}
	return fmt.Sprintf("%d blocked session(s), %d root blocker(s), %d long transaction(s), connection usage %s.",
		s.BlockedSessions, len(s.Roots), s.LongTransactions, ratio)
}

// lifecycle events are always recorded, even after truncation.
func isLifecycle(kind string) bool {
	switch kind {
	case KindTrigger, KindRetriggered, KindResolved, KindRestarted, KindTruncated, KindGap:
		return true
	}
	return false
}

// capEvents drops non-lifecycle events beyond the limits and adds a single
// truncation marker the first time a limit is hit.
func (r *recorder) capEvents(evs []repository.IncidentEvent, count int, bytes int64) ([]repository.IncidentEvent, bool) {
	var out []repository.IncidentEvent
	truncated := false
	for _, e := range evs {
		size := int64(len(e.Payload) + len(e.Summary))
		if !isLifecycle(e.Kind) && (count >= r.cfg.MaxEvents || bytes+size > r.cfg.MaxBytes) {
			if !truncated {
				truncated = true
				out = append(out, repository.IncidentEvent{At: e.At, Kind: KindTruncated,
					Summary: fmt.Sprintf("Incident size limit reached (%d events or %d bytes); further samples are not stored, lifecycle events still are.", r.cfg.MaxEvents, r.cfg.MaxBytes)})
				count++
			}
			continue
		}
		out = append(out, e)
		count++
		bytes += size
	}
	return out, truncated
}

func eventsSize(evs []repository.IncidentEvent) int64 {
	var n int64
	for _, e := range evs {
		n += int64(len(e.Payload) + len(e.Summary))
	}
	return n
}

func (r *recorder) appendEvents(ctx context.Context, oi *openIncident, now time.Time, evs []repository.IncidentEvent, seen bool) error {
	if oi.truncated {
		kept := evs[:0]
		for _, e := range evs {
			if isLifecycle(e.Kind) && e.Kind != KindTruncated {
				kept = append(kept, e)
			}
		}
		evs = kept
	} else {
		var trunc bool
		evs, trunc = r.capEvents(evs, oi.events, oi.bytes)
		if trunc {
			oi.truncated = true
			if err := r.store.UpdateIncidentState(ctx, oi.id, "open", nil, nil, oi.severity, oi.title, true); err != nil {
				return err
			}
		}
	}
	var last *time.Time
	if seen {
		last = &now
	}
	if len(evs) == 0 && last == nil {
		return nil
	}
	if err := r.store.AppendIncidentEvents(ctx, oi.id, last, evs); err != nil {
		return err
	}
	oi.events += len(evs)
	oi.bytes += eventsSize(evs)
	return nil
}
