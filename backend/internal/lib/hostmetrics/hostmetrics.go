// Package hostmetrics reads an optional Prometheus node_exporter endpoint
// configured for a target and derives a few host-level values.
//
// Host metrics are optional: LockLens works without them, and when no
// exporter is configured or it cannot be reached the values are reported as
// unavailable rather than invented. The exporter describes the machine (or
// container VM) it runs on; mapping it to the right PostgreSQL host is the
// operator's responsibility.
package hostmetrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MaxResponseBytes bounds how much of an exporter response is read.
const MaxResponseBytes = 8 << 20

// wanted lists the metric families kept from a scrape; everything else is
// discarded while parsing so a large exporter cannot exhaust memory.
var wanted = map[string]bool{
	"node_cpu_seconds_total":            true,
	"node_memory_MemTotal_bytes":        true,
	"node_memory_MemAvailable_bytes":    true,
	"node_filesystem_size_bytes":        true,
	"node_filesystem_avail_bytes":       true,
	"node_disk_read_bytes_total":        true,
	"node_disk_written_bytes_total":     true,
	"node_network_receive_bytes_total":  true,
	"node_network_transmit_bytes_total": true,
}

// Sample is one series value.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Scrape is the parsed subset of one exporter response.
type Scrape struct {
	At      time.Time
	Samples []Sample
}

// ValidateURL accepts only absolute http(s) URLs without credentials.
func ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("node_exporter URL must be an absolute http or https URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("node_exporter URL must use http or https")
	}
	if u.User != nil {
		return errors.New("node_exporter URL must not contain credentials")
	}
	return nil
}

// Fetch scrapes url with the given client (which should carry a timeout).
func Fetch(ctx context.Context, client *http.Client, rawURL string) (Scrape, error) {
	if err := ValidateURL(rawURL); err != nil {
		return Scrape{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Scrape{}, err
	}
	req.Header.Set("Accept", "text/plain;version=0.0.4")
	resp, err := client.Do(req)
	if err != nil {
		return Scrape{}, fmt.Errorf("scrape node_exporter: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Scrape{}, fmt.Errorf("scrape node_exporter: HTTP %d", resp.StatusCode)
	}
	s, err := Parse(io.LimitReader(resp.Body, MaxResponseBytes+1))
	if err != nil {
		return Scrape{}, err
	}
	s.At = time.Now().UTC()
	return s, nil
}

// ErrTooLarge is returned when a response exceeds MaxResponseBytes.
var ErrTooLarge = errors.New("node_exporter response exceeds the size limit")

// Parse reads the Prometheus text exposition format, keeping wanted families.
func Parse(r io.Reader) (Scrape, error) {
	var s Scrape
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	var read int64
	for sc.Scan() {
		line := sc.Text()
		read += int64(len(line)) + 1
		if read > MaxResponseBytes {
			return s, ErrTooLarge
		}
		if line == "" || line[0] == '#' {
			continue
		}
		name, rest := splitName(line)
		if !wanted[name] {
			continue
		}
		labels, rest, err := parseLabels(rest)
		if err != nil {
			return s, fmt.Errorf("parse node_exporter line: %w", err)
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return s, errors.New("parse node_exporter line: missing value")
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			return s, fmt.Errorf("parse node_exporter value: %w", err)
		}
		s.Samples = append(s.Samples, Sample{Name: name, Labels: labels, Value: v})
	}
	return s, sc.Err()
}

func splitName(line string) (string, string) {
	for i, r := range line {
		if r == '{' || r == ' ' || r == '\t' {
			return line[:i], line[i:]
		}
	}
	return line, ""
}

func parseLabels(s string) (map[string]string, string, error) {
	labels := map[string]string{}
	s = strings.TrimLeft(s, " \t")
	if !strings.HasPrefix(s, "{") {
		return labels, s, nil
	}
	i := 1
	for {
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			return nil, "", errors.New("unterminated label set")
		}
		if s[i] == '}' {
			return labels, s[i+1:], nil
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 {
			return nil, "", errors.New("label without value")
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 1
		if i >= len(s) || s[i] != '"' {
			return nil, "", errors.New("label value must be quoted")
		}
		i++
		var b strings.Builder
		for {
			if i >= len(s) {
				return nil, "", errors.New("unterminated label value")
			}
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				switch s[i+1] {
				case 'n':
					b.WriteByte('\n')
				default:
					b.WriteByte(s[i+1])
				}
				i += 2
				continue
			}
			if c == '"' {
				i++
				break
			}
			b.WriteByte(c)
			i++
		}
		labels[key] = b.String()
	}
}

// Derived holds host values computed from two scrapes. Nil means unavailable.
type Derived struct {
	CPUUtilization *float64
	MemTotal       *float64
	MemAvailable   *float64
	Filesystems    []Filesystem
	DiskReadBps    *float64
	DiskWriteBps   *float64
	NetRxBps       *float64
	NetTxBps       *float64
}

// Filesystem is one real (non-virtual) filesystem.
type Filesystem struct {
	Mountpoint, FSType string
	Size, Avail        float64
}

// virtualFS are pseudo filesystems excluded from capacity reporting.
var virtualFS = map[string]bool{"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true,
	"proc": true, "sysfs": true, "cgroup": true, "cgroup2": true, "nsfs": true, "autofs": true, "ramfs": true}

// Derive computes values from cur and, for rates, the previous scrape.
// Counter decreases (exporter or host restart) make the rate unavailable.
func Derive(prev *Scrape, cur Scrape) Derived {
	var d Derived
	sum := func(s Scrape, name string, skip func(map[string]string) bool) (float64, bool) {
		var total float64
		found := false
		for _, x := range s.Samples {
			if x.Name == name && (skip == nil || !skip(x.Labels)) {
				total += x.Value
				found = true
			}
		}
		return total, found
	}
	if v, ok := sum(cur, "node_memory_MemTotal_bytes", nil); ok {
		d.MemTotal = &v
	}
	if v, ok := sum(cur, "node_memory_MemAvailable_bytes", nil); ok {
		d.MemAvailable = &v
	}
	sizes := map[string]Filesystem{}
	for _, x := range cur.Samples {
		if x.Name != "node_filesystem_size_bytes" && x.Name != "node_filesystem_avail_bytes" {
			continue
		}
		if virtualFS[x.Labels["fstype"]] {
			continue
		}
		key := x.Labels["mountpoint"]
		fs := sizes[key]
		fs.Mountpoint, fs.FSType = key, x.Labels["fstype"]
		if x.Name == "node_filesystem_size_bytes" {
			fs.Size = x.Value
		} else {
			fs.Avail = x.Value
		}
		sizes[key] = fs
	}
	for _, fs := range sizes {
		if fs.Size > 0 {
			d.Filesystems = append(d.Filesystems, fs)
		}
	}
	if prev == nil {
		return d
	}
	elapsed := cur.At.Sub(prev.At).Seconds()
	if elapsed <= 0 {
		return d
	}
	rate := func(name string, skip func(map[string]string) bool) *float64 {
		c, ok1 := sum(cur, name, skip)
		p, ok2 := sum(*prev, name, skip)
		if !ok1 || !ok2 || c < p {
			return nil
		}
		v := (c - p) / elapsed
		return &v
	}
	idle := func(l map[string]string) bool { return l["mode"] != "idle" && l["mode"] != "iowait" }
	all := func(map[string]string) bool { return false }
	idleNow, ok1 := sum(cur, "node_cpu_seconds_total", idle)
	idlePrev, ok2 := sum(*prev, "node_cpu_seconds_total", idle)
	totNow, ok3 := sum(cur, "node_cpu_seconds_total", all)
	totPrev, ok4 := sum(*prev, "node_cpu_seconds_total", all)
	if ok1 && ok2 && ok3 && ok4 && totNow > totPrev && idleNow >= idlePrev {
		u := 1 - (idleNow-idlePrev)/(totNow-totPrev)
		u = min(max(u, 0), 1)
		d.CPUUtilization = &u
	}
	loopback := func(l map[string]string) bool { return l["device"] == "lo" }
	d.DiskReadBps = rate("node_disk_read_bytes_total", nil)
	d.DiskWriteBps = rate("node_disk_written_bytes_total", nil)
	d.NetRxBps = rate("node_network_receive_bytes_total", loopback)
	d.NetTxBps = rate("node_network_transmit_bytes_total", loopback)
	return d
}
