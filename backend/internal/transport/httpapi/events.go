package httpapi

import (
	"fmt"
	"net/http"
	"time"
)

// serveEvents streams the events published on this server to a browser as
// server-sent events until it disconnects.
func (s *Server) serveEvents(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	c, done, ok := s.d.Stream.Subscribe()
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "too_many_clients", "too many event stream clients")
		return
	}
	defer done()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "retry: 5000\n\n")
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-c:
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Kind, e.Data); err != nil {
				return
			}
			fl.Flush()
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
