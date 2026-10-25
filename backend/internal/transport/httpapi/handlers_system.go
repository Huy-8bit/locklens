package httpapi

import (
	"net/http"
)

func (s *Server) storageStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.d.Store.TableSizes(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tables": st})
}
