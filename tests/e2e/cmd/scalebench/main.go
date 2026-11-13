// Command scalebench drives the scaling benchmark (scripts/bench/scale.sh):
//
//	scalebench setup   -url http://127.0.0.1:PORT -targets 300 -hosts 10
//	scalebench measure -nodes URL1,URL2 -store DSN -containers a,b -warmup 3m -duration 10m -out run.json
//
// setup creates targets through the real API (spread over the target
// instances). measure waits for the warm-up, then compares two snapshots of
// every server's /metrics, of the monitoring store's statistics, and of the
// containers' CPU and memory, sampling gauges every 5 seconds in between.
// Percentiles come from histogram buckets (linear interpolation inside the
// bucket, as Prometheus' histogram_quantile), so they are approximate.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"

	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: scalebench setup|measure [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "setup":
		err = setup(os.Args[2:])
	case "measure":
		err = measure(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "scalebench:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------------------
// setup

func setup(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	base := fs.String("url", "", "API base URL of one server")
	n := fs.Int("targets", 20, "targets to create")
	hosts := fs.Int("hosts", 10, "target instances (locklens-bench-target-1..N)")
	interval := fs.Int("interval", 10, "activity interval of every target (seconds)")
	_ = fs.Parse(args)

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar, Timeout: 60 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "bench-admin-password"})
	resp, err := c.Post(*base+"/api/v1/auth/login", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	var sess struct {
		CSRF string `json:"csrf_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&sess)
	resp.Body.Close()
	if err != nil || sess.CSRF == "" {
		return fmt.Errorf("login failed (status %d)", resp.StatusCode)
	}

	type job struct{ i int }
	jobs := make(chan job)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for j := range jobs {
				host := fmt.Sprintf("locklens-bench-target-%d", j.i%*hosts+1)
				in := map[string]any{
					"name":             fmt.Sprintf("bench-%04d", j.i+1),
					"dsn":              fmt.Sprintf("postgres://locklens_observer:bench-observer-pw@%s:5432/postgres?sslmode=disable", host),
					"interval_seconds": *interval,
					"environment":      "bench",
					"max_connections":  1,
				}
				raw, _ := json.Marshal(in)
				req, _ := http.NewRequest(http.MethodPost, *base+"/api/v1/targets", bytes.NewReader(raw))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("X-CSRF-Token", sess.CSRF)
				resp, err := c.Do(req)
				if err == nil {
					msg, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
						err = fmt.Errorf("create %s: %d %s", in["name"], resp.StatusCode, msg)
					}
				}
				if err != nil {
					mu.Lock()
					firstErr = cmpErr(firstErr, err)
					mu.Unlock()
				}
			}
		})
	}
	for i := range *n {
		jobs <- job{i}
	}
	close(jobs)
	wg.Wait()
	if firstErr == nil {
		fmt.Printf("created %d targets over %d instances\n", *n, *hosts)
	}
	return firstErr
}

func cmpErr(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Prometheus text parsing

type sample struct {
	name   string
	labels map[string]string
	value  float64
}

type scrape []sample

func parseMetrics(r io.Reader) (scrape, error) {
	var out scrape
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		var s sample
		rest := line
		if i := strings.IndexByte(line, '{'); i >= 0 {
			s.name = line[:i]
			j := strings.LastIndexByte(line, '}')
			s.labels = parseLabels(line[i+1 : j])
			rest = strings.TrimSpace(line[j+1:])
		} else {
			parts := strings.Fields(line)
			s.name, rest = parts[0], parts[1]
		}
		v, err := strconv.ParseFloat(strings.Fields(rest)[0], 64)
		if err != nil {
			continue
		}
		s.value = v
		out = append(out, s)
	}
	return out, sc.Err()
}

func parseLabels(s string) map[string]string {
	m := map[string]string{}
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		k := strings.TrimSpace(s[:eq])
		s = s[eq+2:] // skip ="
		var b strings.Builder
		i := 0
		for ; i < len(s); i++ {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
				continue
			}
			if s[i] == '"' {
				break
			}
			b.WriteByte(s[i])
		}
		m[k] = b.String()
		s = strings.TrimPrefix(s[min(i+1, len(s)):], ",")
	}
	return m
}

// sum adds the values of a metric over all series matching the filter.
func (sc scrape) sum(name string, filter map[string]string) float64 {
	var t float64
	for _, s := range sc {
		if s.name == name && matches(s.labels, filter) {
			t += s.value
		}
	}
	return t
}

func matches(l, filter map[string]string) bool {
	for k, v := range filter {
		if l[k] != v {
			return false
		}
	}
	return true
}

// labelValues lists the values of one label of a metric.
func (sc scrape) labelValues(name, label string) []string {
	seen := map[string]bool{}
	for _, s := range sc {
		if s.name == name && s.labels[label] != "" {
			seen[s.labels[label]] = true
		}
	}
	out := make([]string, 0, len(seen))
	for v := range seen {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// hist is a histogram aggregated over series: upper bounds and cumulative counts.
type hist struct {
	bounds []float64
	counts []float64
	count  float64
	sum    float64
}

func (sc scrape) histogram(name string, filter map[string]string) hist {
	byLE := map[float64]float64{}
	var h hist
	for _, s := range sc {
		if !matches(s.labels, filter) {
			continue
		}
		switch s.name {
		case name + "_bucket":
			le := s.labels["le"]
			b := math.Inf(1)
			if le != "+Inf" {
				b, _ = strconv.ParseFloat(le, 64)
			}
			byLE[b] += s.value
		case name + "_count":
			h.count += s.value
		case name + "_sum":
			h.sum += s.value
		}
	}
	for b := range byLE {
		h.bounds = append(h.bounds, b)
	}
	slices.Sort(h.bounds)
	for _, b := range h.bounds {
		h.counts = append(h.counts, byLE[b])
	}
	return h
}

func (h hist) minus(o hist) hist {
	out := hist{bounds: h.bounds, count: h.count - o.count, sum: h.sum - o.sum}
	for i, b := range h.bounds {
		prev := 0.0
		if j := slices.Index(o.bounds, b); j >= 0 {
			prev = o.counts[j]
		}
		out.counts = append(out.counts, h.counts[i]-prev)
	}
	return out
}

// quantile interpolates linearly inside the bucket; +Inf answers the last finite bound.
func (h hist) quantile(q float64) float64 {
	if h.count <= 0 || len(h.bounds) == 0 {
		return math.NaN()
	}
	rank := q * h.count
	lower, prevCount := 0.0, 0.0
	for i, b := range h.bounds {
		if h.counts[i] >= rank {
			if math.IsInf(b, 1) {
				return lower
			}
			in := h.counts[i] - prevCount
			if in <= 0 {
				return b
			}
			return lower + (b-lower)*(rank-prevCount)/in
		}
		if !math.IsInf(b, 1) {
			lower = b
		}
		prevCount = h.counts[i]
	}
	return lower
}

func (h hist) mean() float64 {
	if h.count <= 0 {
		return math.NaN()
	}
	return h.sum / h.count
}

// ---------------------------------------------------------------------------
// measure

type storeStats struct {
	At          time.Time
	WALBytes    float64 // pg_current_wal_lsn() as bytes
	Commits     float64
	TupInserted float64
	TupUpdated  float64
	BlksWritten float64
}

type dockerSample struct {
	CPUPct float64
	MemMiB float64
}

func measure(args []string) error {
	fs := flag.NewFlagSet("measure", flag.ExitOnError)
	nodesFlag := fs.String("nodes", "", "comma-separated base URLs of the servers")
	dsn := fs.String("store", "", "monitoring store DSN (from the host)")
	containers := fs.String("containers", "", "comma-separated containers to sample (role=name,…)")
	warmup := fs.Duration("warmup", 3*time.Minute, "time before the measured window")
	duration := fs.Duration("duration", 10*time.Minute, "measured window")
	out := fs.String("out", "", "JSON result file")
	label := fs.String("label", "", "run label, e.g. n300-nodes3-local")
	_ = fs.Parse(args)
	nodes := strings.Split(*nodesFlag, ",")
	ctx := context.Background()

	fmt.Printf("warm-up %s…\n", *warmup)
	time.Sleep(*warmup)

	conn, err := pgx.Connect(ctx, *dsn)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer conn.Close(ctx)

	s0, err := snapshot(nodes)
	if err != nil {
		return err
	}
	st0, err := readStore(ctx, conn)
	if err != nil {
		return err
	}

	// Sample gauges and containers during the window.
	type gaugeAgg struct{ max, sum, n float64 }
	gauges := map[string]*gaugeAgg{}
	addGauge := func(k string, v float64) {
		g := gauges[k]
		if g == nil {
			g = &gaugeAgg{max: v}
			gauges[k] = g
		}
		g.max = math.Max(g.max, v)
		g.sum += v
		g.n++
	}
	dockerSamples := map[string][]dockerSample{}
	var storeConnsMax map[string]float64 = map[string]float64{}
	end := time.Now().Add(*duration)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	ticks := 0
	for time.Now().Before(end) {
		<-tick.C
		ticks++
		for i, u := range nodes {
			sc, err := scrapeNode(u)
			if err != nil {
				continue
			}
			p := fmt.Sprintf("node%d.", i+1)
			addGauge(p+"write_queue_length", sc.sum("locklens_write_queue_length", nil))
			addGauge(p+"slots_busy", sc.sum("locklens_collect_slots_busy", nil))
			addGauge(p+"pool_acquired", sc.sum("locklens_store_pool_conns", map[string]string{"state": "acquired"}))
			addGauge(p+"bus_queue_length", sc.sum("locklens_bus_queue_length", nil))
			addGauge(p+"active_targets", sc.sum("locklens_active_targets", nil))
			addGauge(p+"heap_mib", sc.sum("go_memstats_heap_alloc_bytes", nil)/(1<<20))
		}
		if ticks%3 == 0 {
			for role, ds := range dockerStats(*containers) {
				dockerSamples[role] = append(dockerSamples[role], ds)
			}
			rows, err := conn.Query(ctx, `SELECT coalesce(application_name, ''), count(*) FROM pg_stat_activity WHERE datname = current_database() GROUP BY 1`)
			if err == nil {
				for rows.Next() {
					var app string
					var n float64
					if rows.Scan(&app, &n) == nil {
						storeConnsMax[app] = math.Max(storeConnsMax[app], n)
					}
				}
				rows.Close()
			}
		}
	}

	s1, err := snapshot(nodes)
	if err != nil {
		return err
	}
	st1, err := readStore(ctx, conn)
	if err != nil {
		return err
	}
	secs := st1.At.Sub(st0.At).Seconds()

	res := map[string]any{"label": *label, "window_seconds": math.Round(secs), "nodes": len(nodes)}
	all0, all1 := merge(s0), merge(s1)

	// Collection: runs, lag (absolute and relative to the interval in effect), slot wait, throttling.
	coll := map[string]any{}
	for _, c := range all1.labelValues("locklens_collect_total", "collector") {
		f := map[string]string{"collector": c}
		ok := all1.sum("locklens_collect_total", map[string]string{"collector": c, "outcome": "ok"}) - all0.sum("locklens_collect_total", map[string]string{"collector": c, "outcome": "ok"})
		er := all1.sum("locklens_collect_total", map[string]string{"collector": c, "outcome": "error"}) - all0.sum("locklens_collect_total", map[string]string{"collector": c, "outcome": "error"})
		lag := all1.histogram("locklens_collect_lag_seconds", f).minus(all0.histogram("locklens_collect_lag_seconds", f))
		ratio := all1.histogram("locklens_collect_lag_ratio", f).minus(all0.histogram("locklens_collect_lag_ratio", f))
		wait := all1.histogram("locklens_collect_slot_wait_seconds", f).minus(all0.histogram("locklens_collect_slot_wait_seconds", f))
		dur := all1.histogram("locklens_collect_duration_seconds", f).minus(all0.histogram("locklens_collect_duration_seconds", f))
		up := all1.sum("locklens_collect_throttle_total", map[string]string{"collector": c, "direction": "up"}) - all0.sum("locklens_collect_throttle_total", map[string]string{"collector": c, "direction": "up"})
		coll[c] = map[string]any{
			"runs_per_s": round(ok/secs, 2), "errors": er,
			"duration_mean_ms": ms(dur.mean()), "duration_p99_ms": ms(dur.quantile(0.99)),
			"lag_p50_ms": ms(lag.quantile(0.5)), "lag_p99_ms": ms(lag.quantile(0.99)),
			"lag_ratio_p50": round(ratio.quantile(0.5), 3), "lag_ratio_p99": round(ratio.quantile(0.99), 3),
			"slot_wait_p99_ms": ms(wait.quantile(0.99)), "throttled_up": up,
		}
	}
	allRatio := all1.histogram("locklens_collect_lag_ratio", nil).minus(all0.histogram("locklens_collect_lag_ratio", nil))
	res["collect"] = coll
	res["lag_ratio_p99_all"] = round(allRatio.quantile(0.99), 3)

	// Writer.
	wl := all1.histogram("locklens_store_write_seconds", nil).minus(all0.histogram("locklens_store_write_seconds", nil))
	qw := all1.histogram("locklens_store_queue_wait_seconds", nil).minus(all0.histogram("locklens_store_queue_wait_seconds", nil))
	dropped := map[string]float64{}
	for _, r := range all1.labelValues("locklens_store_dropped_ops_total", "reason") {
		dropped[r] = all1.sum("locklens_store_dropped_ops_total", map[string]string{"reason": r}) - all0.sum("locklens_store_dropped_ops_total", map[string]string{"reason": r})
	}
	res["writer"] = map[string]any{
		"ops_per_s":    round(wl.count/secs, 2),
		"write_p50_ms": ms(wl.quantile(0.5)), "write_p99_ms": ms(wl.quantile(0.99)),
		"busy_fraction":     round(wl.sum/secs/float64(len(nodes)), 3),
		"queue_wait_p50_ms": ms(qw.quantile(0.5)), "queue_wait_p99_ms": ms(qw.quantile(0.99)),
		"dropped":      dropped,
		"write_errors": all1.sum("locklens_store_write_errors_total", nil) - all0.sum("locklens_store_write_errors_total", nil),
	}

	// Direct store writes, pool, cluster, live state, bus.
	sw := all1.histogram("locklens_store_status_write_seconds", nil).minus(all0.histogram("locklens_store_status_write_seconds", nil))
	ra := all1.histogram("locklens_recorder_analyze_seconds", nil).minus(all0.histogram("locklens_recorder_analyze_seconds", nil))
	acq := all1.sum("locklens_store_pool_acquires_total", nil) - all0.sum("locklens_store_pool_acquires_total", nil)
	emp := all1.sum("locklens_store_pool_empty_acquires_total", nil) - all0.sum("locklens_store_pool_empty_acquires_total", nil)
	aw := all1.sum("locklens_store_pool_acquire_wait_seconds_total", nil) - all0.sum("locklens_store_pool_acquire_wait_seconds_total", nil)
	res["store_direct"] = map[string]any{
		"status_writes_per_s": round(sw.count/secs, 2), "status_write_p99_ms": ms(sw.quantile(0.99)),
		"analyze_per_s": round(ra.count/secs, 2), "analyze_p99_ms": ms(ra.quantile(0.99)),
	}
	res["pool"] = map[string]any{
		"acquires_per_s": round(acq/secs, 1), "waited_share": round(safeDiv(emp, acq), 4),
		"mean_acquire_wait_ms": ms(safeDiv(aw, acq)),
	}
	as := all1.histogram("locklens_cluster_assign_seconds", nil).minus(all0.histogram("locklens_cluster_assign_seconds", nil))
	sy := all1.histogram("locklens_manager_sync_seconds", nil).minus(all0.histogram("locklens_manager_sync_seconds", nil))
	res["cluster"] = map[string]any{
		"assign_p99_ms": ms(as.quantile(0.99)), "assign_max_bucket_ms": ms(maxBucket(as)),
		"assign_errors": all1.sum("locklens_cluster_assign_errors_total", nil) - all0.sum("locklens_cluster_assign_errors_total", nil),
		"sync_p99_ms":   ms(sy.quantile(0.99)),
	}
	lp := all1.histogram("locklens_live_publish_seconds", nil).minus(all0.histogram("locklens_live_publish_seconds", nil))
	lv := all1.histogram("locklens_live_view_bytes", nil).minus(all0.histogram("locklens_live_view_bytes", nil))
	lr := all1.histogram("locklens_live_remote_read_seconds", nil).minus(all0.histogram("locklens_live_remote_read_seconds", nil))
	res["live_state"] = map[string]any{
		"publish_tick_p99_ms": ms(lp.quantile(0.99)),
		"bytes_per_s":         round((all1.sum("locklens_live_publish_bytes_total", nil)-all0.sum("locklens_live_publish_bytes_total", nil))/secs, 0),
		"view_kib_p50":        round(lv.quantile(0.5)/1024, 1), "view_kib_p99": round(lv.quantile(0.99)/1024, 1),
		"errors":             all1.sum("locklens_live_publish_errors_total", nil) - all0.sum("locklens_live_publish_errors_total", nil),
		"remote_read_p99_ms": ms(lr.quantile(0.99)),
	}
	busDropped := map[string]float64{}
	for _, r := range all1.labelValues("locklens_bus_dropped_total", "reason") {
		busDropped[r] = all1.sum("locklens_bus_dropped_total", map[string]string{"reason": r}) - all0.sum("locklens_bus_dropped_total", map[string]string{"reason": r})
	}
	res["bus"] = map[string]any{
		"sent_per_s": round((all1.sum("locklens_bus_sent_total", nil)-all0.sum("locklens_bus_sent_total", nil))/secs, 1),
		"dropped":    busDropped,
	}

	// Store.
	var liveRows, liveTotal, liveAvg, liveMax float64
	_ = conn.QueryRow(ctx, `SELECT count(*), coalesce(pg_total_relation_size('live_state'), 0),
		coalesce(avg(pg_column_size(view)), 0), coalesce(max(pg_column_size(view)), 0) FROM live_state`).Scan(&liveRows, &liveTotal, &liveAvg, &liveMax)
	var dbSize float64
	_ = conn.QueryRow(ctx, `SELECT pg_database_size(current_database())`).Scan(&dbSize)
	res["store"] = map[string]any{
		"wal_mib_per_s":           round((st1.WALBytes-st0.WALBytes)/secs/(1<<20), 3),
		"commits_per_s":           round((st1.Commits-st0.Commits)/secs, 1),
		"tup_inserted_per_s":      round((st1.TupInserted-st0.TupInserted)/secs, 1),
		"tup_updated_per_s":       round((st1.TupUpdated-st0.TupUpdated)/secs, 1),
		"connections_max":         storeConnsMax,
		"db_size_mib":             round(dbSize/(1<<20), 1),
		"live_state_rows":         liveRows,
		"live_state_total_kib":    round(liveTotal/1024, 0),
		"live_state_view_avg_kib": round(liveAvg/1024, 1),
		"live_state_view_max_kib": round(liveMax/1024, 1),
	}

	// Gauges and containers.
	g := map[string]any{}
	for k, a := range gauges {
		g[k] = map[string]any{"max": round(a.max, 1), "mean": round(a.sum/a.n, 1)}
	}
	res["gauges"] = g
	dk := map[string]any{}
	for role, ss := range dockerSamples {
		var cpu, mem, memMax float64
		for _, s := range ss {
			cpu += s.CPUPct
			mem += s.MemMiB
			memMax = math.Max(memMax, s.MemMiB)
		}
		dk[role] = map[string]any{"cpu_pct_mean": round(cpu/float64(len(ss)), 1), "mem_mib_mean": round(mem/float64(len(ss)), 1), "mem_mib_max": round(memMax, 1)}
	}
	res["containers"] = dk

	raw, _ := json.MarshalIndent(res, "", "  ")
	if *out != "" {
		if err := os.WriteFile(*out, raw, 0o644); err != nil {
			return err
		}
	}
	fmt.Println(string(raw))
	return nil
}

func snapshot(nodes []string) ([]scrape, error) {
	out := make([]scrape, len(nodes))
	for i, u := range nodes {
		sc, err := scrapeNode(u)
		if err != nil {
			return nil, fmt.Errorf("scrape %s: %w", u, err)
		}
		out[i] = sc
	}
	return out, nil
}

func merge(ss []scrape) scrape {
	var out scrape
	for _, s := range ss {
		out = append(out, s...)
	}
	return out
}

func scrapeNode(base string) (scrape, error) {
	c := &http.Client{Timeout: 10 * time.Second}
	resp, err := c.Get(base + "/metrics")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	return parseMetrics(resp.Body)
}

func readStore(ctx context.Context, conn *pgx.Conn) (storeStats, error) {
	var s storeStats
	err := conn.QueryRow(ctx, `SELECT now(), pg_wal_lsn_diff(pg_current_wal_lsn(), '0/0'),
		xact_commit, tup_inserted, tup_updated, blks_hit + blks_read
		FROM pg_stat_database WHERE datname = current_database()`).
		Scan(&s.At, &s.WALBytes, &s.Commits, &s.TupInserted, &s.TupUpdated, &s.BlksWritten)
	return s, err
}

// dockerStats samples CPU and memory of role=container pairs.
func dockerStats(spec string) map[string]dockerSample {
	out := map[string]dockerSample{}
	if spec == "" {
		return out
	}
	byName := map[string]string{}
	var names []string
	for _, p := range strings.Split(spec, ",") {
		role, name, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		byName[name] = role
		names = append(names, name)
	}
	cmd := exec.Command("docker", append([]string{"stats", "--no-stream", "--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}"}, names...)...)
	raw, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			continue
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(f[1], "%"), 64)
		out[byName[f[0]]] = dockerSample{CPUPct: cpu, MemMiB: parseMem(strings.TrimSpace(strings.Split(f[2], "/")[0]))}
	}
	return out
}

func parseMem(s string) float64 {
	units := []struct {
		suffix string
		mib    float64
	}{{"GiB", 1024}, {"MiB", 1}, {"KiB", 1.0 / 1024}, {"B", 1.0 / (1 << 20)}}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, u.suffix), 64)
			if err != nil {
				return 0
			}
			return v * u.mib
		}
	}
	return 0
}

func maxBucket(h hist) float64 {
	m := 0.0
	prev := 0.0
	for i, b := range h.bounds {
		if h.counts[i] > prev && !math.IsInf(b, 1) {
			m = b
		}
		prev = h.counts[i]
	}
	if len(h.counts) > 0 && h.count > h.counts[len(h.counts)-1] {
		return math.Inf(1)
	}
	return m
}

func safeDiv(a, b float64) float64 {
	if b == 0 {
		return 0
	}
	return a / b
}

func ms(sec float64) any {
	if math.IsNaN(sec) {
		return nil
	}
	if math.IsInf(sec, 1) {
		return "inf"
	}
	return round(sec*1000, 2)
}

func round(v float64, d int) any {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	p := math.Pow(10, float64(d))
	return math.Round(v*p) / p
}
