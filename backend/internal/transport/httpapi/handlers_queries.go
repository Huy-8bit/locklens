package httpapi

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"

	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

type queryItem struct {
	repository.QueryRow
	MeanMS      *float64          `json:"mean_exec_ms"`
	CallsPerSec *float64          `json:"calls_per_second"`
	PrevMeanMS  *float64          `json:"previous_mean_exec_ms"`
	Comparison  *rules.Comparison `json:"comparison"`
}

func (s *Server) listQueries(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	limit, offset, err := page(r)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q := r.URL.Query()
	sort := q.Get("sort")
	if sort == "" {
		sort = "total_time"
	}
	if !repository.ValidQuerySort(sort) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "unknown sort key")
		return
	}
	compare := q.Get("compare") == "true"
	filter := q.Get("q")
	if len(filter) > 200 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "filter is too long")
		return
	}
	// Text is returned when the target collects it, unless the client asks
	// for include_text=false.
	includeText := q.Get("include_text") != "false" && t.QueryTextEnabled
	command := q.Get("command")
	if command != "" && !slices.Contains(observe.Commands, command) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "unknown command class")
		return
	}
	// LockLens' own monitoring statements are real load but rarely what an
	// investigation is about; they are hidden unless asked for.
	exclude := t.User
	if q.Get("include_monitoring") == "true" {
		exclude = ""
	}
	rows, total, err := s.d.Store.ListQueries(r.Context(), repository.QueryListParams{TargetID: t.ID, From: from, To: to,
		Compare: compare, Filter: filter, IncludeText: includeText, Sort: sort, Desc: q.Get("order") != "asc",
		Limit: limit, Offset: offset, ExcludeUser: exclude, Command: command})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	th := s.d.Settings.Thresholds()
	win := to.Sub(from).Seconds()
	items := make([]queryItem, 0, len(rows))
	for _, row := range rows {
		it := queryItem{QueryRow: row}
		cur := rules.WindowAgg{Calls: row.Calls, TotalMS: row.TotalMS, Rows: row.Rows}
		it.MeanMS = cur.MeanMS()
		cps := float64(row.Calls) / win
		it.CallsPerSec = &cps
		if compare {
			prev := rules.WindowAgg{}
			if row.PrevCalls != nil {
				prev.Calls = *row.PrevCalls
			}
			if row.PrevTotalMS != nil {
				prev.TotalMS = *row.PrevTotalMS
			}
			it.PrevMeanMS = prev.MeanMS()
			c := rules.Compare(prev, cur, th)
			it.Comparison = &c
		}
		items = append(items, it)
	}
	cov, err := s.d.Store.StatementCoverage(r.Context(), t.ID, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp := map[string]any{"from": from, "to": to, "items": items, "total": total, "page_size": limit,
		"coverage": cov, "query_text_enabled": t.QueryTextEnabled, "text_included": includeText,
		"min_calls": th.RegressionMinCalls, "monitoring_user": t.User, "monitoring_hidden": exclude != ""}
	if compare {
		prevCov, err := s.d.Store.StatementCoverage(r.Context(), t.ID, from.Add(-to.Sub(from)), from)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		resp["previous_coverage"] = prevCov
	}
	if v, ok := s.view(t.ID); ok && v.Capabilities != nil {
		resp["available"] = v.Capabilities.StatStatements
		resp["unavailable_reason"] = v.Capabilities.StatementsReason
	} else if t.Status.Capabilities != nil {
		resp["available"] = t.Status.Capabilities.StatStatements
		resp["unavailable_reason"] = t.Status.Capabilities.StatementsReason
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) getQuery(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	qid, ok := pathID(r, "qid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid query identity")
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	includeText := r.URL.Query().Get("include_text") != "false" && t.QueryTextEnabled
	ident, first, last, err := s.d.Store.QueryIdentity(r.Context(), t.ID, qid, includeText)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "query identity not found")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	step, err := metricStep(r, from, to, s.statementsStep(t))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	step = s.d.Store.QueryStep(from, step)
	buckets, err := s.d.Store.QueryBuckets(r.Context(), t.ID, qid, from, to, step)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	window := to.Sub(from)
	cc, ct, cr, err := s.d.Store.WindowTotals(r.Context(), t.ID, qid, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	pc, pt, pr, err := s.d.Store.WindowTotals(r.Context(), t.ID, qid, from.Add(-window), from)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cur := rules.WindowAgg{Calls: cc, TotalMS: ct, Rows: cr}
	prev := rules.WindowAgg{Calls: pc, TotalMS: pt, Rows: pr}
	covCur, err := s.d.Store.StatementCoverage(r.Context(), t.ID, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	covPrev, err := s.d.Store.StatementCoverage(r.Context(), t.ID, from.Add(-window), from)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"identity": ident, "first_seen": first, "last_seen": last, "from": from, "to": to, "step_seconds": step.Seconds(),
		"buckets": buckets, "query_text_enabled": t.QueryTextEnabled, "text_included": includeText,
		"current":    map[string]any{"from": from, "to": to, "totals": cur, "mean_exec_ms": cur.MeanMS(), "coverage": covCur},
		"previous":   map[string]any{"from": from.Add(-window), "to": from, "totals": prev, "mean_exec_ms": prev.MeanMS(), "coverage": covPrev},
		"comparison": rules.Compare(prev, cur, s.d.Settings.Thresholds()),
	})
}

// queryCommands breaks the workload down by statement command class
// (select, insert, update, ...): totals for the window and a time series.
func (s *Server) queryCommands(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	exclude := t.User
	if r.URL.Query().Get("include_monitoring") == "true" {
		exclude = ""
	}
	step := stepFor(from, to, s.statementsStep(t))
	totals, buckets, coverage, err := s.d.Store.CommandBreakdown(r.Context(), t.ID, from, to, step, exclude)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	cov, err := s.d.Store.StatementCoverage(r.Context(), t.ID, from, to)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	resp := map[string]any{"from": from, "to": to, "step_seconds": step.Seconds(), "commands": observe.Commands,
		"totals": totals, "buckets": buckets, "coverage_buckets": coverage, "coverage": cov, "monitoring_user": t.User, "monitoring_hidden": exclude != ""}
	if v, ok := s.view(t.ID); ok && v.Capabilities != nil {
		resp["available"] = v.Capabilities.StatStatements
		resp["unavailable_reason"] = v.Capabilities.StatementsReason
	} else if t.Status.Capabilities != nil {
		resp["available"] = t.Status.Capabilities.StatStatements
		resp["unavailable_reason"] = t.Status.Capabilities.StatementsReason
	}
	writeJSON(w, http.StatusOK, resp)
}
