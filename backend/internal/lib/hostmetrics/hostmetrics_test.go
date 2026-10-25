package hostmetrics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func scrape(t *testing.T, at time.Time, cpuIdle, cpuUser, diskRead, netLo, netEth float64) Scrape {
	t.Helper()
	text := fmt.Sprintf(`# HELP node_cpu_seconds_total Seconds the CPUs spent in each mode.
# TYPE node_cpu_seconds_total counter
node_cpu_seconds_total{cpu="0",mode="idle"} %g
node_cpu_seconds_total{cpu="0",mode="user"} %g
node_cpu_seconds_total{cpu="0",mode="iowait"} 0
node_memory_MemTotal_bytes 8.589934592e+09
node_memory_MemAvailable_bytes 4.294967296e+09
node_filesystem_size_bytes{device="/dev/vda1",fstype="ext4",mountpoint="/"} 1e+11
node_filesystem_avail_bytes{device="/dev/vda1",fstype="ext4",mountpoint="/"} 2.5e+10
node_filesystem_size_bytes{device="tmpfs",fstype="tmpfs",mountpoint="/run"} 1e+08
node_filesystem_avail_bytes{device="tmpfs",fstype="tmpfs",mountpoint="/run"} 1e+08
node_disk_read_bytes_total{device="vda"} %g
node_disk_written_bytes_total{device="vda"} 0
node_network_receive_bytes_total{device="lo"} %g
node_network_receive_bytes_total{device="eth0"} %g
node_network_transmit_bytes_total{device="eth0"} 0
node_uname_info{machine="x86_64",nodename="db \"one\""} 1
go_goroutines 7
`, cpuIdle, cpuUser, diskRead, netLo, netEth)
	s, err := Parse(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	s.At = at
	return s
}

func TestParseKeepsOnlyWantedFamilies(t *testing.T) {
	s := scrape(t, time.Now(), 100, 50, 0, 0, 0)
	for _, x := range s.Samples {
		if !wanted[x.Name] {
			t.Errorf("unwanted family kept: %s", x.Name)
		}
	}
	if len(s.Samples) != 14 {
		t.Errorf("want 14 samples, got %d", len(s.Samples))
	}
	if _, err := Parse(strings.NewReader("node_memory_MemTotal_bytes not-a-number\n")); err == nil {
		t.Error("invalid value must fail")
	}
	if _, err := Parse(strings.NewReader(`node_cpu_seconds_total{mode="idle" 5` + "\n")); err == nil {
		t.Error("unterminated labels must fail")
	}
}

func TestDeriveRatesAndCapacity(t *testing.T) {
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	prev := scrape(t, t0, 100, 100, 1000, 0, 1000)
	cur := scrape(t, t0.Add(10*time.Second), 106, 104, 21000, 5000, 6000)
	d := Derive(&prev, cur)
	// CPU: 10 s elapsed, 6 s idle, 4 s user -> 40% busy.
	if d.CPUUtilization == nil || math.Abs(*d.CPUUtilization-0.4) > 1e-9 {
		t.Errorf("cpu = %v", d.CPUUtilization)
	}
	if d.DiskReadBps == nil || *d.DiskReadBps != 2000 {
		t.Errorf("disk read = %v", d.DiskReadBps)
	}
	// Loopback traffic is excluded from network throughput.
	if d.NetRxBps == nil || *d.NetRxBps != 500 {
		t.Errorf("net rx = %v", d.NetRxBps)
	}
	if d.MemTotal == nil || *d.MemTotal != 8589934592 || d.MemAvailable == nil {
		t.Errorf("memory = %v %v", d.MemTotal, d.MemAvailable)
	}
	if len(d.Filesystems) != 1 || d.Filesystems[0].Mountpoint != "/" {
		t.Errorf("virtual filesystems must be excluded: %+v", d.Filesystems)
	}
}

func TestDeriveUnknownWithoutPreviousOrAfterReset(t *testing.T) {
	t0 := time.Now()
	first := Derive(nil, scrape(t, t0, 100, 100, 1000, 0, 1000))
	if first.CPUUtilization != nil || first.DiskReadBps != nil || first.NetRxBps != nil {
		t.Error("rates need two scrapes")
	}
	if first.MemTotal == nil {
		t.Error("gauges are available from one scrape")
	}
	prev := scrape(t, t0, 100, 100, 5000, 0, 5000)
	cur := scrape(t, t0.Add(10*time.Second), 1, 1, 10, 0, 10) // exporter or host restarted
	d := Derive(&prev, cur)
	if d.CPUUtilization != nil || d.DiskReadBps != nil || d.NetRxBps != nil {
		t.Errorf("counter resets must make rates unknown, got %+v", d)
	}
}

func TestValidateURL(t *testing.T) {
	for u, ok := range map[string]bool{
		"http://db1:9100/metrics":             true,
		"https://db1.example:9100/metrics":    true,
		"ftp://db1/metrics":                   false,
		"file:///etc/passwd":                  false,
		"db1:9100/metrics":                    false,
		"http://user:secret@db1:9100/metrics": false,
		"/metrics":                            false,
	} {
		if err := ValidateURL(u); (err == nil) != ok {
			t.Errorf("ValidateURL(%q) = %v, want ok=%v", u, err, ok)
		}
	}
}

func TestFetchErrorsAndSizeLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			http.Error(w, "nope", http.StatusInternalServerError)
		case "/huge":
			line := strings.Repeat("x", 1000) + " 1\n"
			for range MaxResponseBytes/len(line) + 10 {
				_, _ = w.Write([]byte(line))
			}
		default:
			_, _ = w.Write([]byte("node_memory_MemTotal_bytes 1024\n"))
		}
	}))
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()
	s, err := Fetch(ctx, client, srv.URL+"/metrics")
	if err != nil || len(s.Samples) != 1 || s.At.IsZero() {
		t.Fatalf("fetch: %+v %v", s, err)
	}
	if _, err := Fetch(ctx, client, srv.URL+"/fail"); err == nil || !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("non-200: %v", err)
	}
	if _, err := Fetch(ctx, client, srv.URL+"/huge"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("oversized response: %v", err)
	}
}
