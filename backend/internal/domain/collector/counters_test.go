package collector

import (
	"testing"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func sample(at time.Duration, start time.Time, scopes map[string]observe.ScopeCounters) observe.CounterSample {
	return observe.CounterSample{At: t0.Add(at), ServerStart: start, Scopes: scopes}
}

func scope(reset *time.Time, kv ...any) observe.ScopeCounters {
	v := map[string]float64{}
	for i := 0; i < len(kv); i += 2 {
		v[kv[i].(string)] = float64(kv[i+1].(int))
	}
	return observe.ScopeCounters{StatsReset: reset, Values: v}
}

func TestDeltaAndRate(t *testing.T) {
	prev := sample(0, t0, map[string]observe.ScopeCounters{"1": scope(nil, "commit", 100), "2": scope(nil, "commit", 50)})
	cur := sample(10*time.Second, t0, map[string]observe.ScopeCounters{"1": scope(nil, "commit", 160), "2": scope(nil, "commit", 70)})
	d := Delta(&prev, cur, "commit", time.Minute)
	if d.Delta == nil || *d.Delta != 80 || *d.Rate() != 8 {
		t.Fatalf("delta = %+v", d)
	}
}

func TestDeltaUnknownCases(t *testing.T) {
	reset1 := t0.Add(-time.Hour)
	reset2 := t0.Add(5 * time.Second)
	base := sample(0, t0, map[string]observe.ScopeCounters{"1": scope(&reset1, "c", 100)})
	cases := map[string]struct {
		prev *observe.CounterSample
		cur  observe.CounterSample
		want string
	}{
		"first sample": {nil, base, ReasonFirstSample},
		"restart":      {&base, sample(10*time.Second, t0.Add(time.Second), map[string]observe.ScopeCounters{"1": scope(&reset1, "c", 5)}), ReasonRestart},
		"stats reset":  {&base, sample(10*time.Second, t0, map[string]observe.ScopeCounters{"1": scope(&reset2, "c", 5)}), ReasonStatsReset},
		"negative":     {&base, sample(10*time.Second, t0, map[string]observe.ScopeCounters{"1": scope(&reset1, "c", 99)}), ReasonNegative},
		"gap":          {&base, sample(10*time.Minute, t0, map[string]observe.ScopeCounters{"1": scope(&reset1, "c", 200)}), ReasonGap},
		"no common":    {&base, sample(10*time.Second, t0, map[string]observe.ScopeCounters{"2": scope(&reset1, "c", 200)}), ReasonNoScopes},
	}
	for name, tc := range cases {
		d := Delta(tc.prev, tc.cur, "c", time.Minute)
		if d.Delta != nil || d.Reason != tc.want || d.Rate() != nil {
			t.Errorf("%s: got %+v, want unknown with %s", name, d, tc.want)
		}
	}
}

func TestDeltaIgnoresCreatedAndDroppedScopes(t *testing.T) {
	prev := sample(0, t0, map[string]observe.ScopeCounters{"1": scope(nil, "c", 10), "dropped": scope(nil, "c", 1000)})
	cur := sample(5*time.Second, t0, map[string]observe.ScopeCounters{"1": scope(nil, "c", 15), "created": scope(nil, "c", 500)})
	d := Delta(&prev, cur, "c", time.Minute)
	if d.Delta == nil || *d.Delta != 5 {
		t.Fatalf("delta = %+v, want 5 (only the common scope)", d)
	}
}

func TestZeroDeltaIsKnownZero(t *testing.T) {
	prev := sample(0, t0, map[string]observe.ScopeCounters{"": scope(nil, "c", 7)})
	cur := sample(5*time.Second, t0, map[string]observe.ScopeCounters{"": scope(nil, "c", 7)})
	d := Delta(&prev, cur, "c", time.Minute)
	if d.Delta == nil || *d.Delta != 0 {
		t.Fatalf("an unchanged counter is a known zero, got %+v", d)
	}
}

func TestRatio(t *testing.T) {
	v := func(f float64) DeltaResult { return DeltaResult{Delta: &f} }
	if r := Ratio(v(90), v(10)); r == nil || *r != 0.9 {
		t.Errorf("ratio = %v", r)
	}
	if r := Ratio(v(0), v(0)); r != nil {
		t.Error("no activity must give an undefined ratio, not 1 or 0")
	}
	if r := Ratio(DeltaResult{Reason: ReasonGap}, v(1)); r != nil {
		t.Error("unknown input must give unknown ratio")
	}
}
