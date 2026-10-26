// Package load turns active session samples into database load: the average
// number of sessions that were active (AAS), over time and per group (wait
// class, wait event, statement, user, database, application, backend type).
//
// AAS of a group over a period is the number of times its sessions were seen
// divided by the number of samples taken. A period without samples is a gap,
// never zero load. Sessions idle in a transaction are not load: they appear
// as their own class when grouping by class or event, and are left out of
// every other grouping unless asked for.
package load

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// Point is the AAS of one time bucket; V is nil when no sample was taken.
type Point struct {
	T time.Time `json:"t"`
	V *float64  `json:"v"`
}

// Series is one group's AAS over time. Key "_other" sums the groups beyond
// the top ones.
type Series struct {
	Key    string  `json:"key"`
	Label  string  `json:"label"`
	Points []Point `json:"points"`
}

// Group is one group's load over the whole window.
type Group struct {
	Key string `json:"key"`
	// Values are the dimension's values (see repository load columns).
	Values []string `json:"values"`
	Label  string   `json:"label"`
	AAS    float64  `json:"aas"`
	// Share is the group's part of the load shown in this grouping.
	Share *float64 `json:"share"`
	// Classes breaks the group's AAS down by wait class.
	Classes map[string]float64 `json:"classes"`
}

// Result is the load of one window grouped by one dimension.
type Result struct {
	Dimension string `json:"dimension"`
	// Ticks is the number of samples taken in the window.
	Ticks int64 `json:"ticks"`
	// ActiveAAS is the average number of active sessions in the window
	// (idle-in-transaction sessions excluded); nil without samples.
	ActiveAAS *float64 `json:"active_aas"`
	// IdleTxAAS is the average number of sessions idle in a transaction.
	IdleTxAAS *float64 `json:"idle_in_transaction_aas"`
	Series    []Series `json:"series"`
	Top       []Group  `json:"top"`
}

// keyLen is how many leading values identify a group of a dimension (the
// query dimension also carries database and user names as labels).
func keyLen(dim repository.LoadDimension, values []string) int {
	if dim == repository.LoadByQuery {
		return min(3, len(values))
	}
	return len(values)
}

func groupKey(dim repository.LoadDimension, r repository.LoadRow) string {
	switch dim {
	case repository.LoadByClass:
		return r.WaitClass
	case repository.LoadByEvent:
		return r.WaitClass + "/" + strings.Join(r.Key, "")
	}
	return strings.Join(r.Key[:keyLen(dim, r.Key)], "/")
}

func label(dim repository.LoadDimension, class string, values []string) string {
	switch dim {
	case repository.LoadByClass:
		return ClassLabel(class)
	case repository.LoadByEvent:
		if len(values) == 0 || values[0] == "" {
			return ClassLabel(class)
		}
		return class + ": " + values[0]
	case repository.LoadByQuery:
		if len(values) >= 3 && values[2] != "0" {
			return "queryid " + values[2]
		}
		return "Statements without a query ID"
	}
	if len(values) == 0 || values[0] == "" {
		return "(none)"
	}
	return values[0]
}

// ClassLabel names a wait class for display.
func ClassLabel(class string) string {
	switch class {
	case observe.WaitCPU:
		return "CPU (not waiting)"
	case observe.WaitIdleTx:
		return "Idle in transaction"
	}
	return class
}

// Build aggregates rows (per step bucket) into series of the top groups and
// a table of the top groups. includeIdleTx keeps idle-in-transaction samples
// in groupings other than class and event.
func Build(dim repository.LoadDimension, rows []repository.LoadRow, ticks []repository.LoadTicks, top int, includeIdleTx bool) Result {
	res := Result{Dimension: string(dim), Series: []Series{}, Top: []Group{}}
	byClass := dim == repository.LoadByClass || dim == repository.LoadByEvent
	tickAt := map[time.Time]int64{}
	for _, t := range ticks {
		tickAt[t.T] = t.Ticks
		res.Ticks += t.Ticks
	}
	if res.Ticks == 0 {
		return res
	}
	type acc struct {
		g       Group
		samples int64
		classes map[string]int64
		at      map[time.Time]int64
	}
	groups := map[string]*acc{}
	var active, idle, grouped int64
	for _, r := range rows {
		if r.WaitClass == observe.WaitIdleTx {
			idle += r.Samples
		} else {
			active += r.Samples
		}
		if r.WaitClass == observe.WaitIdleTx && !byClass && !includeIdleTx {
			continue
		}
		k := groupKey(dim, r)
		a := groups[k]
		if a == nil {
			a = &acc{g: Group{Key: k, Values: r.Key, Label: label(dim, r.WaitClass, r.Key)}, classes: map[string]int64{}, at: map[time.Time]int64{}}
			groups[k] = a
		}
		a.samples += r.Samples
		grouped += r.Samples
		a.classes[r.WaitClass] += r.Samples
		a.at[r.T] += r.Samples
	}
	n := float64(res.Ticks)
	res.ActiveAAS, res.IdleTxAAS = ptr(float64(active)/n), ptr(float64(idle)/n)

	list := make([]*acc, 0, len(groups))
	for _, a := range groups {
		list = append(list, a)
	}
	slices.SortFunc(list, func(a, b *acc) int {
		if c := cmp.Compare(b.samples, a.samples); c != 0 {
			return c
		}
		return cmp.Compare(a.g.Key, b.g.Key)
	})
	for _, a := range list {
		g := a.g
		g.AAS = float64(a.samples) / n
		g.Share = ptr(float64(a.samples) / float64(grouped))
		g.Classes = map[string]float64{}
		for c, s := range a.classes {
			g.Classes[c] = float64(s) / n
		}
		res.Top = append(res.Top, g)
	}
	if len(res.Top) > 50 {
		res.Top = res.Top[:50]
	}

	// Series: the top groups, the rest summed as "Other".
	times := make([]time.Time, 0, len(tickAt))
	for t := range tickAt {
		times = append(times, t)
	}
	slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
	series := func(key, lbl string, at func(time.Time) int64) Series {
		s := Series{Key: key, Label: lbl, Points: make([]Point, 0, len(times))}
		for _, t := range times {
			s.Points = append(s.Points, Point{T: t, V: ptr(float64(at(t)) / float64(tickAt[t]))})
		}
		return s
	}
	for i, a := range list {
		if i == top {
			rest := list[top:]
			res.Series = append(res.Series, series("_other", "Other", func(t time.Time) int64 {
				var x int64
				for _, o := range rest {
					x += o.at[t]
				}
				return x
			}))
			break
		}
		res.Series = append(res.Series, series(a.g.Key, a.g.Label, func(t time.Time) int64 { return a.at[t] }))
	}
	return res
}

func ptr(v float64) *float64 { return &v }
