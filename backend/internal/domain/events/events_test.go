package events

import "testing"

func TestHubDeliversToSubscribers(t *testing.T) {
	n := 0
	h := NewHub(1, func(c int) { n = c })
	c, done, ok := h.Subscribe()
	if !ok || n != 1 {
		t.Fatalf("subscribe %v %d", ok, n)
	}
	if _, _, ok := h.Subscribe(); ok {
		t.Fatal("at most one subscriber")
	}
	Fanout{h}.Publish("incident.updated", map[string]int{"incident_id": 7})
	if e := <-c; e.Kind != "incident.updated" || string(e.Data) != `{"incident_id":7}` {
		t.Fatalf("event %+v", e)
	}
	done()
	if n != 0 {
		t.Fatalf("count after unsubscribe %d", n)
	}
}
