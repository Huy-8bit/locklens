package export

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/finding"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/domain/report"
	"github.com/locklens/locklens/internal/lib/render"
	"github.com/locklens/locklens/internal/lib/textsafe"
)

//go:embed report.html.tmpl
var templates embed.FS

var reportTmpl = template.Must(template.New("report.html.tmpl").Funcs(template.FuncMap{
	"clean": func(s string) string { return textsafe.Terminal(s) },
	"time":  fmtTime,
	"ptime": func(t *time.Time) string {
		if t == nil {
			return "—"
		}
		return fmtTime(*t)
	},
	"pstr": func(s *string) string {
		if s == nil {
			return "—"
		}
		return textsafe.Terminal(*s)
	},
	"value": fmtValue,
	"lower": strings.ToLower,
}).ParseFS(templates, "report.html.tmpl"))

func fmtTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05 UTC") }

func fmtValue(v any, unit string) string {
	switch x := v.(type) {
	case nil:
		return "unknown"
	case float64:
		switch unit {
		case "s":
			return render.Duration(x)
		case "bytes":
			return fmtBytes(x)
		case "ratio":
			return fmt.Sprintf("%.1f%%", x*100)
		case "ms":
			return fmt.Sprintf("%.2f ms", x)
		}
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d %s", int64(x), unit)
		}
		return fmt.Sprintf("%.3f %s", x, unit)
	case string:
		return textsafe.Terminal(x)
	default:
		b, _ := json.Marshal(x)
		return textsafe.Terminal(string(b)) + " " + unit
	}
}

func fmtBytes(b float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", b, units[i])
}

// TreeNode is one node of a rendered blocking tree.
type TreeNode struct {
	Label    string
	Details  []string
	Note     string
	Children []TreeNode
}

// Graph is one blocking snapshot rendered for the report.
type Graph struct {
	At       time.Time
	Summary  string
	Roots    []TreeNode
	Warnings []string
}

type timelineRow struct {
	At      time.Time
	Kind    string
	Summary string
	Metrics []kv
	Queries []queryRow
}

type kv struct{ K, V string }

type queryRow struct {
	QueryID  string
	Database string
	User     string
	Calls    string
	Total    string
	Mean     string
	Share    string
	Text     string
}

type viewModel struct {
	E        Incident
	Duration string
	Lead     []finding.Finding
	Findings []finding.Finding
	Timeline []timelineRow
	Graphs   []Graph
}

// WriteHTML renders a standalone report. All styles are inline; no script,
// font, or stylesheet is loaded from anywhere.
func WriteHTML(w io.Writer, e Incident) error {
	vm := viewModel{E: e}
	end := time.Now().UTC()
	if e.Incident.ResolvedAt != nil {
		end = *e.Incident.ResolvedAt
	} else {
		end = e.Incident.LastSeenAt
	}
	vm.Duration = render.Duration(end.Sub(e.Incident.OpenedAt).Seconds())
	for _, f := range e.Findings {
		vm.Findings = append(vm.Findings, f.Finding)
	}
	for _, ev := range e.Events {
		row := timelineRow{At: ev.At, Kind: ev.Kind, Summary: ev.Summary}
		var p map[string]json.RawMessage
		_ = json.Unmarshal(ev.Payload, &p)
		switch ev.Kind {
		case "trigger", "finding":
			var f finding.Finding
			if json.Unmarshal(p["finding"], &f) == nil && f.RuleID != "" {
				if ev.Kind == "trigger" {
					vm.Lead = append(vm.Lead, f)
				}
				if len(e.Findings) == 0 {
					vm.Findings = append(vm.Findings, f)
				}
			}
		case "sample", "pre_trigger", "post_trigger":
			var m map[string]*float64
			_ = json.Unmarshal(p["metrics"], &m)
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := "unknown"
				if m[k] != nil {
					v = fmt.Sprintf("%.4g", *m[k])
				}
				row.Metrics = append(row.Metrics, kv{k, v})
			}
		case "workload":
			var cur []struct {
				Key struct {
					QueryID string `json:"queryid"`
				} `json:"key"`
				DatName  string   `json:"datname"`
				UserName string   `json:"username"`
				Text     *string  `json:"text"`
				Calls    int64    `json:"calls"`
				TotalMS  float64  `json:"total_exec_ms"`
				MeanMS   *float64 `json:"mean_exec_ms"`
				Share    *float64 `json:"share_of_total_time"`
			}
			_ = json.Unmarshal(p["current"], &cur)
			for _, q := range cur {
				r := queryRow{QueryID: q.Key.QueryID, Database: q.DatName, User: q.UserName,
					Calls: fmt.Sprint(q.Calls), Total: fmt.Sprintf("%.1f ms", q.TotalMS), Mean: "—", Share: "—"}
				if q.MeanMS != nil {
					r.Mean = fmt.Sprintf("%.2f ms", *q.MeanMS)
				}
				if q.Share != nil {
					r.Share = fmt.Sprintf("%.1f%%", *q.Share*100)
				}
				if q.Text != nil {
					r.Text = *q.Text
				}
				row.Queries = append(row.Queries, r)
			}
		case "blocking_snapshot":
			var snap model.Snapshot
			if json.Unmarshal(p["snapshot"], &snap) == nil && !snap.ServerTime.IsZero() {
				vm.Graphs = append(vm.Graphs, buildGraph(&snap, ev.At))
			}
		}
		vm.Timeline = append(vm.Timeline, row)
	}
	return reportTmpl.Execute(w, vm)
}

// buildGraph renders a snapshot as trees, printing shared descendants once
// and stopping at cycles, like the CLI.
func buildGraph(snap *model.Snapshot, at time.Time) Graph {
	rep := report.Build(snap, time.Duration(snap.Collection.MinTxAgeSeconds*float64(time.Second)))
	g := Graph{At: at, Summary: fmt.Sprintf("%d blocked, %d blocking, %d root blocker(s)",
		rep.Analysis.Summary.BlockedSessions, rep.Analysis.Summary.BlockingSessions, rep.Analysis.Summary.RootBlockers)}
	for _, w := range append(snap.Warnings, rep.Analysis.Warnings...) {
		g.Warnings = append(g.Warnings, w.Message)
	}
	sessions := map[int]*model.Session{}
	for i := range snap.Sessions {
		sessions[snap.Sessions[i].PID] = &snap.Sessions[i]
	}
	ages := map[int]report.SessionAges{}
	for _, a := range rep.Analysis.Sessions {
		ages[a.PID] = a
	}
	nodes := map[int]report.Node{}
	for _, n := range rep.Analysis.Nodes {
		nodes[n.ID] = n
	}
	var build func(pid int, shown, path map[int]bool) TreeNode
	build = func(pid int, shown, path map[int]bool) TreeNode {
		n := nodes[pid]
		tn := TreeNode{Label: fmt.Sprintf("pid %d", pid)}
		if pid == 0 {
			tn.Label = "prepared transaction (PID 0)"
		}
		if path[pid] {
			tn.Note = "cycle back to an ancestor"
			return tn
		}
		if shown[pid] {
			tn.Note = "shown above"
			return tn
		}
		shown[pid], path[pid] = true, true
		defer delete(path, pid)
		if s, ok := sessions[pid]; ok {
			a := ages[pid]
			age := func(v *float64) string {
				if v == nil {
					return "unknown"
				}
				return render.Duration(*v)
			}
			state := "unknown"
			if s.State != nil {
				state = *s.State
			}
			tn.Details = append(tn.Details, "state: "+state, "transaction age: "+age(a.TransactionAgeSeconds))
			if len(n.BlockedBy) > 0 {
				tn.Details = append(tn.Details, "lock wait: "+age(a.LockWaitSeconds))
			}
			if !s.DetailsVisible {
				tn.Details = append(tn.Details, "details hidden by permissions")
			}
			if s.User != nil {
				tn.Details = append(tn.Details, "user: "+*s.User)
			}
			if s.ApplicationName != nil && *s.ApplicationName != "" {
				tn.Details = append(tn.Details, "application: "+*s.ApplicationName)
			}
			for _, l := range s.WaitingLocks {
				tn.Details = append(tn.Details, "waiting for "+render.DescribeLock(l))
			}
			if s.Query != nil {
				tn.Details = append(tn.Details, "query: "+*s.Query)
			}
		} else if n.Kind == report.KindMissing {
			tn.Details = append(tn.Details, "no pg_stat_activity row in this collection")
		}
		if n.DirectBlocked > 0 {
			tn.Details = append(tn.Details, fmt.Sprintf("blocks %d directly, %d in total", n.DirectBlocked, n.TransitiveBlocked))
		}
		for _, c := range n.Blocks {
			tn.Children = append(tn.Children, build(c, shown, path))
		}
		return tn
	}
	for _, c := range rep.Analysis.Components {
		starts := c.Roots
		if c.Cyclic {
			starts = c.Entries
			if len(starts) == 0 {
				starts = c.CycleMembers[:1]
			}
		}
		shown := map[int]bool{}
		for _, s := range starts {
			g.Roots = append(g.Roots, build(s, shown, map[int]bool{}))
		}
	}
	return g
}
