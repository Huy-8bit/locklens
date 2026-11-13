package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// report turns the JSON results of a directory into the Markdown tables of
// docs/test-report.md, and checks the success criteria on the goal run.
func report(args []string) error {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	dir := fs.String("dir", "test-results/bench", "directory of <label>.json results")
	goal := fs.String("goal", "n300-nodes3", "label prefix of the run the success criteria apply to")
	_ = fs.Parse(args)

	files, _ := filepath.Glob(filepath.Join(*dir, "n*-nodes*-*.json"))
	type run struct {
		n, nodes int
		profile  string
		label    string
		r        map[string]any
	}
	re := regexp.MustCompile(`^n(\d+)-nodes(\d+)-(\w+)$`)
	var runs []run
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		var r map[string]any
		if err := json.Unmarshal(raw, &r); err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		label := strings.TrimSuffix(filepath.Base(f), ".json")
		m := re.FindStringSubmatch(label)
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		nodes, _ := strconv.Atoi(m[2])
		runs = append(runs, run{n, nodes, m[3], label, r})
	}
	sort.Slice(runs, func(i, j int) bool {
		a, b := runs[i], runs[j]
		if a.profile != b.profile {
			return a.profile < b.profile
		}
		if a.n != b.n {
			return a.n < b.n
		}
		return a.nodes < b.nodes
	})

	fmt.Println("#### Collection and writes")
	fmt.Println()
	fmt.Println("| Run | Lag p99 (worst collector, × interval) | Slow-downs | Slot wait p99 | Writer ops/s | Write p99 | Queue wait p99 | Queue max | Dropped ops | Status writes/s |")
	fmt.Println("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, x := range runs {
		worst, worstName, throttles, slotWait := 0.0, "", 0.0, 0.0
		for name, c := range obj(x.r, "collect") {
			cm, _ := c.(map[string]any)
			if v := num(cm["lag_ratio_p99"]); v > worst {
				worst, worstName = v, name
			}
			throttles += num(cm["throttled_up"])
			slotWait = math.Max(slotWait, num(cm["slot_wait_p99_ms"]))
		}
		w := obj(x.r, "writer")
		qmax := 0.0
		for k, g := range obj(x.r, "gauges") {
			if strings.HasSuffix(k, ".write_queue_length") {
				qmax = math.Max(qmax, num(g.(map[string]any)["max"]))
			}
		}
		fmt.Printf("| %s | %.2f (%s) | %.0f | %s ms | %s | %s ms | %s ms | %.0f | %s | %s |\n", x.label, worst, worstName, throttles, f1(slotWait),
			f1(num(w["ops_per_s"])), f1(num(w["write_p99_ms"])), f1(num(w["queue_wait_p99_ms"])), qmax, drops(w["dropped"]),
			f1(num(obj(x.r, "store_direct")["status_writes_per_s"])))
	}
	fmt.Println()
	fmt.Println("#### Store, cluster, and servers")
	fmt.Println()
	fmt.Println("| Run | Store CPU % | Store WAL MiB/s | Commits/s | Pool waits | Assign p99 | Lease changes | live_state KiB/s | View KiB p50/p99 | Bus msg/s | Bus dropped | Server CPU % (mean) | Server memory MiB (max) |")
	fmt.Println("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
	for _, x := range runs {
		st, cl, ls, bus, pool := obj(x.r, "store"), obj(x.r, "cluster"), obj(x.r, "live_state"), obj(x.r, "bus"), obj(x.r, "pool")
		var cpu, mem float64
		nodes := 0
		for k, c := range obj(x.r, "containers") {
			if strings.HasPrefix(k, "node") {
				cm := c.(map[string]any)
				cpu += num(cm["cpu_pct_mean"])
				mem = math.Max(mem, num(cm["mem_mib_max"]))
				nodes++
			}
		}
		storeCPU := num(obj(obj(x.r, "containers"), "store")["cpu_pct_mean"])
		fmt.Printf("| %s | %.0f | %s | %s | %.1f%% | %s ms | %s | %s | %s / %s | %s | %s | %.0f | %.0f |\n", x.label, storeCPU, f2(num(st["wal_mib_per_s"])),
			f1(num(st["commits_per_s"])), 100*num(pool["waited_share"]), f1(num(cl["assign_p99_ms"])), drops(cl["lease_changes"]), f1(num(ls["bytes_per_s"])/1024),
			f1(num(ls["view_kib_p50"])), f1(num(ls["view_kib_p99"])), f1(num(bus["sent_per_s"])), drops(bus["dropped"]), cpu/math.Max(1, float64(nodes)), mem)
	}
	fmt.Println()
	for _, x := range runs {
		if !strings.HasPrefix(x.label, *goal) {
			continue
		}
		worst, throttles := 0.0, 0.0
		for _, c := range obj(x.r, "collect") {
			cm, _ := c.(map[string]any)
			worst = math.Max(worst, num(cm["lag_ratio_p99"]))
			throttles += num(cm["throttled_up"])
		}
		moves := 0.0
		for _, v := range obj(obj(x.r, "cluster"), "lease_changes") {
			moves += num(v)
		}
		verdict := func(ok bool) string {
			if ok {
				return "pass"
			}
			return "**fail**"
		}
		lag := verdict(worst < 1)
		if worst < 1 && throttles > 0 {
			lag = fmt.Sprintf("pass, with %.0f slow-downs", throttles)
		}
		fmt.Printf("- `%s`: lag p99 %.2f × interval → %s; lease changes in the window %.0f → %s; store WAL %s MiB/s, CPU %.0f%%.\n", x.label, worst, lag, moves, verdict(moves == 0),
			f2(num(obj(x.r, "store")["wal_mib_per_s"])), num(obj(obj(x.r, "containers"), "store")["cpu_pct_mean"]))
	}
	return nil
}

func obj(m map[string]any, k string) map[string]any {
	v, _ := m[k].(map[string]any)
	return v
}

func num(v any) float64 {
	f, ok := v.(float64)
	if !ok {
		return 0
	}
	return f
}

func f1(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func drops(v any) string {
	m, _ := v.(map[string]any)
	if len(m) == 0 {
		return "0"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %.0f", k, num(m[k])))
	}
	return strings.Join(parts, ", ")
}
