package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/locklens/locklens/internal/domain/collector"
	"github.com/locklens/locklens/internal/domain/load"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// targetLoad serves database load (active session sampling) of one target:
// AAS over time for the top groups of a dimension and a table of the top
// groups. Statements are resolved to stored identities, with SQL text when
// the target collects it.
func (s *Server) targetLoad(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	q := r.URL.Query()
	dim := q.Get("by")
	if dim == "" {
		dim = string(repository.LoadByClass)
	}
	if !repository.ValidLoadDimension(dim) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "by must be class, event, query, user, database, application, or backend")
		return
	}
	var f repository.LoadFilter
	if v := q.Get("class"); v != "" {
		if len(v) > 64 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "class is too long")
			return
		}
		f.WaitClass = v
	}
	if v := q.Get("queryid"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "queryid must be an integer")
			return
		}
		f.QueryID = &id
	}
	top := 8
	if v := q.Get("top"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 20 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "top must be between 1 and 20")
			return
		}
		top = n
	}
	// Buckets are 15 s; a step that is not a multiple would alternate between
	// one and two buckets per point.
	step, err := metricStep(r, from, to, observe.LoadBucketSize)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	step = (step + observe.LoadBucketSize - 1) / observe.LoadBucketSize * observe.LoadBucketSize
	rows, ticks, err := s.d.Store.LoadRows(r.Context(), t.ID, from, to, step, repository.LoadDimension(dim), f)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	res := load.Build(repository.LoadDimension(dim), rows, ticks, top, f.WaitClass == observe.WaitIdleTx)

	statements := []repository.QueryRef{}
	if dim == string(repository.LoadByQuery) {
		var refs []repository.StatementRef
		for _, g := range res.Top {
			if len(g.Values) < 3 {
				continue
			}
			dbid, _ := strconv.ParseUint(g.Values[0], 10, 32)
			userid, _ := strconv.ParseUint(g.Values[1], 10, 32)
			qid, _ := strconv.ParseInt(g.Values[2], 10, 64)
			if qid != 0 {
				refs = append(refs, repository.StatementRef{DBID: uint32(dbid), UserID: uint32(userid), QueryID: qid})
			}
		}
		if statements, err = s.d.Store.QueryRefsByActivity(r.Context(), t.ID, refs, t.QueryTextEnabled); err != nil {
			s.internal(w, r, err)
			return
		}
	}

	// Whether sampling runs, and why not, from the collector's status.
	var status *collector.CollectorStatus
	if v, ok := s.view(t.ID); ok {
		if c, ok := v.Collectors[collector.CollectorLoad]; ok {
			status = &c
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from": from, "to": to, "step_seconds": step.Seconds(), "load": res, "statements": statements,
		"query_text_enabled": t.QueryTextEnabled, "collector": status,
	})
}
