package insight

import (
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/baseline"
	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

// Service investigates statement anomalies: it gathers the evidence and ranks hypotheses.
type Service interface {
	// Investigate correlates the evidence around the findings with key subject
	// (a statement "q:..." or an execution "pid:...") of one target, as of at.
	// queryID narrows the wait profile to the statement when known.
	Investigate(ctx context.Context, targetID int64, subject string, queryID *int64, at time.Time) (Investigation, error)
}

var _ Service = (*service)(nil)

// service gathers the signals of an anomaly from the monitoring store.
type service struct {
	store    Repositories
	sampling bool
}

// New returns an insight service; sampling says whether active
// session sampling is enabled.
func New(store Repositories, sampling bool) Service {
	return &service{store: store, sampling: sampling}
}

// IsStatementRule reports whether a rule is about statement performance.
func IsStatementRule(id string) bool {
	return slices.Contains(rules.StatementRules, id) || id == rules.RuleLongRunning
}

// minWindow is the shortest period wait profiles are compared over.
const minWindow = 5 * time.Minute

// Investigate correlates the evidence around the findings with key subject
// (a statement "q:..." or an execution "pid:...") of one target, as of at.
// queryID narrows the wait profile to the statement when known.
func (s *service) Investigate(ctx context.Context, targetID int64, subject string, queryID *int64, at time.Time) (Investigation, error) {
	rows, err := s.store.ListFindings(ctx, repository.FindingFilter{TargetID: targetID, Limit: 500})
	if err != nil {
		return Investigation{}, err
	}
	active := func(r repository.FindingRow, from, to time.Time) bool {
		end := r.LastSeen
		if r.ResolvedAt != nil {
			end = *r.ResolvedAt
		}
		return r.Finding.Status != finding.InsufficientData && !r.FirstSeen.After(to) && !end.Before(from)
	}
	sig := Signals{At: at, SamplingEnabled: s.sampling}
	for _, r := range rows {
		if r.Finding.Key == subject && active(r, at.Add(-time.Minute), at) {
			sig.Subject = append(sig.Subject, Observed{Finding: r.Finding, FirstSeen: r.FirstSeen})
		}
	}
	sig.Since = at
	for _, o := range sig.Subject {
		if o.FirstSeen.Before(sig.Since) {
			sig.Since = o.FirstSeen
		}
	}
	// Other findings within an hour before the anomaly and up to now.
	for _, r := range rows {
		if r.Finding.Key == subject || !active(r, sig.Since.Add(-time.Hour), at) {
			continue
		}
		if r.Finding.RuleID == rules.RuleMeanRegression {
			sig.OtherRegressions++
			continue
		}
		if IsStatementRule(r.Finding.RuleID) {
			continue // other statements' anomalies are not evidence for this one
		}
		sig.Others = append(sig.Others, Observed{Finding: r.Finding, FirstSeen: r.FirstSeen})
	}
	nowFrom := sig.Since
	if at.Sub(nowFrom) < minWindow {
		nowFrom = at.Add(-minWindow)
	}
	span := min(at.Sub(nowFrom), time.Hour)
	if s.sampling {
		if queryID != nil {
			sig.StatementWaits, err = s.waits(ctx, targetID, queryID, nowFrom, at, span)
			if err != nil {
				return Investigation{}, err
			}
		}
		sig.InstanceWaits, err = s.waits(ctx, targetID, nil, nowFrom, at, span)
		if err != nil {
			return Investigation{}, err
		}
	}
	gaps, err := s.store.ListGaps(ctx, targetID, sig.Since.Add(-time.Hour), at, 50)
	if err != nil {
		return Investigation{}, err
	}
	for _, g := range gaps {
		sig.Gaps = append(sig.Gaps, Gap{From: g.From, To: g.To, What: g.Collector})
	}
	sig.Changes, err = s.store.ListChanges(ctx, repository.ChangeFilter{From: sig.Since.Add(-changeLead), To: at.Add(time.Second),
		TargetIDs: []int64{targetID}, Limit: 100})
	if err != nil {
		return Investigation{}, err
	}
	sig.QueryID = queryID
	if sig.Logs, err = s.store.LogEvents(ctx, repository.LogEventFilter{TargetID: targetID, From: sig.Since.Add(-30 * time.Minute), To: at.Add(time.Second),
		Kinds: []string{logs.KindLockWait, logs.KindDeadlock, logs.KindTempFile}, Limit: 500}); err != nil {
		return Investigation{}, err
	}
	agents, err := s.store.LogAgents(ctx, targetID)
	if err != nil {
		return Investigation{}, err
	}
	sig.LogsShipped = len(agents) > 0
	if queryID != nil {
		if n, err := s.store.CountPlans(ctx, targetID, *queryID); err == nil && n > 0 {
			sig.PlanHistory = true
		} else if err != nil {
			return Investigation{}, err
		}
	}
	return Correlate(sig), nil
}

// waits returns average active sessions per wait class over [from, to) and
// the span before it; a period without sampling ticks is nil (unknown).
func (s *service) waits(ctx context.Context, targetID int64, queryID *int64, from, to time.Time, before time.Duration) (*WaitShift, error) {
	avg := func(f, t time.Time) (map[string]float64, error) {
		rows, ticks, err := s.store.LoadRows(ctx, targetID, f, t, t.Sub(f)+time.Second, repository.LoadByClass, repository.LoadFilter{QueryID: queryID})
		if err != nil {
			return nil, err
		}
		var n int64
		for _, tk := range ticks {
			n += tk.Ticks
		}
		if n == 0 {
			return nil, nil
		}
		out := map[string]float64{}
		for _, r := range rows {
			out[r.WaitClass] += float64(r.Samples) / float64(n)
		}
		return out, nil
	}
	now, err := avg(from, to)
	if err != nil {
		return nil, err
	}
	prev, err := avg(from.Add(-before), from)
	if err != nil {
		return nil, err
	}
	if now == nil && prev == nil {
		return nil, nil
	}
	return &WaitShift{Now: now, Before: prev}, nil
}

// BandPoint is the usual range of a measure at one time: the baseline median
// and median ± k robust deviations.
type BandPoint struct {
	T      time.Time `json:"t"`
	Median float64   `json:"median"`
	Low    float64   `json:"low"`
	High   float64   `json:"high"`
	Basis  string    `json:"basis"`
}

// Band returns the usual range of a measure at the given times, following the
// hour-of-day profile where there is one. Times without a baseline value are
// left out.
func Band(b baseline.Baseline, m baseline.Measure, times []time.Time, k float64) []BandPoint {
	out := []BandPoint{}
	for _, t := range times {
		p, basis := b.For(t)
		st, ok := p.Stats[m]
		if !ok || st.N < baseline.MinProfilePoints {
			continue
		}
		sp := st.Spread(0.05 * st.Median)
		out = append(out, BandPoint{T: t, Median: st.Median, Low: math.Max(0, st.Median-k*sp), High: st.Median + k*sp, Basis: string(basis)})
	}
	return out
}

// StatementKey parses a statement finding key
// ("q:major:dbid:userid:toplevel:queryid").
func StatementKey(key string) (observe.QueryKey, bool) {
	var k observe.QueryKey
	parts := strings.Split(key, ":")
	if len(parts) != 6 || parts[0] != "q" {
		return k, false
	}
	major, e1 := strconv.Atoi(parts[1])
	dbid, e2 := strconv.ParseUint(parts[2], 10, 32)
	userid, e3 := strconv.ParseUint(parts[3], 10, 32)
	top, e4 := strconv.ParseBool(parts[4])
	qid, e5 := strconv.ParseInt(parts[5], 10, 64)
	if errors.Join(e1, e2, e3, e4, e5) != nil {
		return k, false
	}
	return observe.QueryKey{ServerMajor: major, DBID: uint32(dbid), UserID: uint32(userid), TopLevel: top, QueryID: qid}, true
}

// BaselineParams derives the baseline configuration from the thresholds. The
// two most recent workload windows are left out of the history, so the period
// a rule evaluates is never part of what it is compared with.
func BaselineParams(th rules.Thresholds) baseline.Params {
	return baseline.Params{Days: th.BaselineDays, Point: time.Duration(th.BaselinePointSeconds) * time.Second,
		MinCalls: th.BaselineMinCallsPerPoint, Recent: time.Duration(2 * th.WorkloadWindowSeconds * float64(time.Second))}
}
