package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Hub fans out server events to SSE clients. Each client has a small buffer;
// a slow client misses events (the UI then refetches) instead of blocking
// collectors.
type Hub struct {
	mu      sync.Mutex
	clients map[chan []byte]struct{}
	max     int
	onCount func(int)
}

// NewHub returns a hub accepting at most max clients.
func NewHub(max int, onCount func(int)) *Hub {
	return &Hub{clients: map[chan []byte]struct{}{}, max: max, onCount: onCount}
}

// Publish implements collector.EventSink.
func (h *Hub) Publish(kind string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	msg := []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", kind, data))
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		select {
		case c <- msg:
		default:
		}
	}
}

func (h *Hub) subscribe() (chan []byte, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.clients) >= h.max {
		return nil, false
	}
	c := make(chan []byte, 32)
	h.clients[c] = struct{}{}
	if h.onCount != nil {
		h.onCount(len(h.clients))
	}
	return c, true
}

func (h *Hub) unsubscribe(c chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
	if h.onCount != nil {
		h.onCount(len(h.clients))
	}
}

// ServeHTTP streams events until the client disconnects.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	c, ok := h.subscribe()
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "too_many_clients", "too many event stream clients")
		return
	}
	defer h.unsubscribe(c)
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
		case msg := <-c:
			if _, err := w.Write(msg); err != nil {
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
