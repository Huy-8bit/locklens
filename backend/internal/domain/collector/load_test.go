package collector

import (
	"fmt"
	"testing"
	"time"
)

func TestLoadAggMergesGroupsBeyondTheLimit(t *testing.T) {
	a := &loadAgg{at: time.Unix(0, 0), groups: map[loadKey]int{}}
	for i := 0; i < maxLoadGroups+50; i++ {
		a.add(loadKey{class: "CPU", backend: "client backend", app: fmt.Sprintf("app-%d", i)})
	}
	a.add(loadKey{class: "CPU", backend: "client backend", app: "app-0"})
	a.ticks = 1
	b := a.bucket(2 * time.Second)
	if len(b.Groups) != maxLoadGroups+1 {
		t.Fatalf("groups = %d, want %d", len(b.Groups), maxLoadGroups+1)
	}
	var total int
	for _, g := range b.Groups {
		total += g.Samples
		if g.Application == "(other)" && g.Samples != 50 {
			t.Fatalf("other = %d, want 50", g.Samples)
		}
		if g.Application == "app-0" && g.Samples != 2 {
			t.Fatalf("app-0 = %d, want 2", g.Samples)
		}
	}
	if total != maxLoadGroups+51 || b.IntervalSeconds != 2 {
		t.Fatalf("total = %d, interval = %v", total, b.IntervalSeconds)
	}
}
