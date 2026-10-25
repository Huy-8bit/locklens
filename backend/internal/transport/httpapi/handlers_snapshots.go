package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/lib/snapshot"
	"github.com/locklens/locklens/internal/repository"
)

func (s *Server) reportFrom(raw json.RawMessage) (*report.Report, error) {
	var snap model.Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, err
	}
	return report.Build(&snap, s.d.Settings.LongTransactionThreshold()), nil
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	from, to, err := timeRange(r, 24*time.Hour)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	limit := 200
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	list, err := s.d.Store.ListActivitySnapshots(r.Context(), t.ID, from, to, r.URL.Query().Get("blocking") == "true", limit)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": list, "from": from, "to": to})
}

func (s *Server) getSnapshot(w http.ResponseWriter, r *http.Request) {
	t, ok := s.targetOr404(w, r)
	if !ok {
		return
	}
	sid, ok := pathID(r, "sid")
	if !ok {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "invalid snapshot id")
		return
	}
	meta, raw, err := s.d.Store.GetActivitySnapshot(r.Context(), t.ID, sid)
	if errors.Is(err, repository.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "snapshot not found (it may have expired)")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	rep, err := s.reportFrom(raw)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"source": "stored", "snapshot_id": meta.ID, "observed_at": meta.At, "report": rep})
}

// analyzeSnapshot renders an uploaded CLI snapshot file (offline analysis).
func (s *Server) analyzeSnapshot(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxImportBytes)
	snap, err := snapshot.Decode(r.Body, "uploaded snapshot")
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", "snapshot exceeds 10 MiB")
			return
		}
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_snapshot", err.Error())
		return
	}
	th := time.Duration(snap.Collection.MinTxAgeSeconds * float64(time.Second))
	writeJSON(w, http.StatusOK, map[string]any{"source": "upload", "observed_at": snap.ServerTime, "report": report.Build(snap, th)})
}
