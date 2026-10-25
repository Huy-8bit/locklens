package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"time"
)

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

// timeRange parses from/to (RFC 3339) or range (Go duration ending now),
// defaulting to the last hour, bounded by MaxRange.
func timeRange(r *http.Request, def time.Duration) (time.Time, time.Time, error) {
	q := r.URL.Query()
	to := time.Now().UTC()
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must be an RFC 3339 timestamp")
		}
		to = t.UTC()
	}
	from := to.Add(-def)
	if v := q.Get("range"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return time.Time{}, time.Time{}, errors.New("range must be a positive duration such as 1h")
		}
		from = to.Add(-d)
	}
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be an RFC 3339 timestamp")
		}
		from = t.UTC()
	}
	if !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	if to.Sub(from) > MaxRange {
		return time.Time{}, time.Time{}, errors.New("time range is limited to 31 days")
	}
	return from, to, nil
}

// niceSteps are bucket sizes for downsampling.
var niceSteps = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 5 * time.Minute, 15 * time.Minute,
	time.Hour, 3 * time.Hour, 6 * time.Hour, 24 * time.Hour}

// stepFor returns the smallest nice step giving at most maxPoints buckets.
func stepFor(from, to time.Time, minStep time.Duration) time.Duration {
	span := to.Sub(from)
	for _, s := range niceSteps {
		if s >= minStep && span/s <= maxPoints {
			return s
		}
	}
	return niceSteps[len(niceSteps)-1]
}

func page(r *http.Request) (limit, offset int, err error) {
	q := r.URL.Query()
	limit, offset = 50, 0
	if v := q.Get("page_size"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > MaxPageSize {
			return 0, 0, errors.New("page_size must be between 1 and 200")
		}
		limit = n
	}
	if v := q.Get("page"); v != "" {
		n, e := strconv.Atoi(v)
		if e != nil || n < 1 || n > 100000 {
			return 0, 0, errors.New("page must be a positive integer")
		}
		offset = (n - 1) * limit
	}
	return limit, offset, nil
}

// metricStep accepts an explicit minimum resolution and a per-series point cap.
func metricStep(r *http.Request, from, to time.Time, interval time.Duration) (time.Duration, error) {
	points := maxPoints
	if raw := r.URL.Query().Get("max_points"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 2 || n > 1000 {
			return 0, errors.New("max_points must be between 2 and 1000")
		}
		points = n
	}
	step := max(interval, time.Second)
	if raw := r.URL.Query().Get("step"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < time.Second || d > MaxRange {
			return 0, errors.New("step must be a duration between 1s and 744h")
		}
		step = max(step, d)
	}
	required := (to.Sub(from) + time.Duration(points) - 1) / time.Duration(points)
	return max(step, ((required+time.Second-1)/time.Second)*time.Second), nil
}
