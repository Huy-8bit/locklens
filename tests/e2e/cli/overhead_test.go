//go:build integration

package integration

import (
	"encoding/json"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/lib/postgres"
)

// TestObserverOverheadSmoke repeatedly collects snapshots from a quiet
// server and records latency, connection, and goroutine figures. It is a
// local smoke check, not a benchmark: it asserts only that resource usage
// stays flat, and logs the numbers for docs/test-report.md.
func TestObserverOverheadSmoke(t *testing.T) {
	const warmup, samples = 20, 300
	ctx := testContext(t)
	adm := admin(t)

	cfg, red, err := config.PoolConfig(observerDSN, config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	c, err := postgres.Connect(ctx, cfg, red, model.Tool{Name: "locklens", Version: "it"})
	if err != nil {
		t.Fatal(err)
	}
	opts := postgres.Options{QueryMaxLength: 200, MinTxAge: time.Minute}
	for range warmup {
		if _, err := c.Collect(ctx, opts); err != nil {
			t.Fatal(err)
		}
	}
	runtime.GC()
	goroutinesBefore := runtime.NumGoroutine()

	connections := func() int {
		var n int
		if err := adm.QueryRow(ctx, "SELECT count(*) FROM pg_stat_activity WHERE application_name = 'locklens'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	client := make([]float64, 0, samples)
	server := make([]float64, 0, samples)
	maxConns := 0
	start := time.Now()
	for i := range samples {
		t0 := time.Now()
		snap, err := c.Collect(ctx, opts)
		if err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		client = append(client, float64(time.Since(t0).Microseconds())/1000)
		server = append(server, float64(snap.Collection.DurationMS))
		if i%25 == 0 {
			maxConns = max(maxConns, connections())
		}
	}
	wall := time.Since(start)
	runtime.GC()
	goroutinesAfter := runtime.NumGoroutine()
	c.Close()

	slices.Sort(client)
	slices.Sort(server)
	pct := func(p float64) float64 { return client[min(len(client)-1, int(p*float64(len(client))))] }
	result := map[string]any{
		"postgres_version":       pgVersion,
		"samples":                samples,
		"warmup":                 warmup,
		"wall_seconds":           wall.Seconds(),
		"latency_ms_min":         client[0],
		"latency_ms_p50":         pct(0.50),
		"latency_ms_p95":         pct(0.95),
		"latency_ms_p99":         pct(0.99),
		"latency_ms_max":         client[len(client)-1],
		"server_duration_ms_p50": server[len(server)/2],
		"server_duration_ms_max": server[len(server)-1],
		"max_locklens_conns":     maxConns,
		"goroutines_after_warm":  goroutinesBefore,
		"goroutines_after_run":   goroutinesAfter,
		"conditions":             "quiet isolated server, sequential collections, no contention, loopback via Docker port publishing",
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("OVERHEAD %s", data)
	writeArtifact(t, "overhead.json", data)

	if maxConns > 2 {
		t.Errorf("collector used %d connections; the pool allows 2", maxConns)
	}
	if goroutinesAfter > goroutinesBefore+2 {
		t.Errorf("goroutines grew from %d to %d", goroutinesBefore, goroutinesAfter)
	}
	deadline := time.Now().Add(5 * time.Second)
	for connections() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d LockLens connections remain after Close", connections())
		}
		time.Sleep(50 * time.Millisecond)
	}
}
