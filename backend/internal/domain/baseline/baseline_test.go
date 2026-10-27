package baseline

import (
	"math"
	"testing"
	"time"
)

func TestRobustMedianAndMAD(t *testing.T) {
	s := Robust([]float64{1, 2, 3, 4, 100})
	if s.Median != 3 || s.MAD != 1 || s.N != 5 {
		t.Fatalf("stat = %+v", s)
	}
	// One extreme value barely moves the median and MAD.
	if z := s.Z(100, 0); z < 60 {
		t.Fatalf("z(100) = %v", z)
	}
	if s := Robust([]float64{2, 4}); s.Median != 3 || s.MAD != 1 {
		t.Fatalf("even count: %+v", s)
	}
	if s := Robust(nil); s.N != 0 {
		t.Fatalf("empty: %+v", s)
	}
}

func TestZeroVarianceUsesTheFloor(t *testing.T) {
	s := Robust([]float64{5, 5, 5, 5})
	if s.MAD != 0 {
		t.Fatalf("mad = %v", s.MAD)
	}
	if z := s.Z(5.1, 0.25); math.Abs(z-0.4) > 1e-9 {
		t.Fatalf("z with floor = %v, want 0.4", z)
	}
	if z := s.Z(6, 0); z != 0 {
		t.Fatalf("no spread and no floor must not divide by zero: %v", z)
	}
}

func hour(day, h int) time.Time {
	return time.Date(2026, 10, day, h, 0, 0, 0, time.UTC)
}

func TestBuildCountsQuietPeriodsAndSkipsResets(t *testing.T) {
	coverage := map[time.Time]float64{}
	var points []Point
	for d := 1; d <= 3; d++ {
		for h := 0; h < 24; h++ {
			coverage[hour(d, h)] = 3600
			if h%2 == 0 { // runs every other hour
				points = append(points, Point{At: hour(d, h), Calls: 3600, TotalMS: 7200, Rows: 3600, BlkRead: 360, BlkHit: 3240})
			}
		}
	}
	// A reset hour with absurd numbers is ignored.
	resets := map[time.Time]bool{hour(2, 4): true}
	points[0+12+2] = Point{At: hour(2, 4), Calls: 1, TotalMS: 1e9}
	b := Build(points, coverage, resets, hour(1, 0), hour(4, 0), 3600, 10)
	if b.All.Points != 71 || b.Days != 3 {
		t.Fatalf("points %d days %d", b.All.Points, b.Days)
	}
	// Quiet hours count as zero calls: half the hours ran at 1/s.
	cps := b.All.Stats[CallsPerSec]
	if cps.N != 71 || cps.Median != 0 && cps.Median != 1 {
		t.Fatalf("calls/s = %+v", cps)
	}
	// Per-call measures come only from hours that ran.
	if m := b.All.Stats[MeanMS]; m.Median != 2 || m.N != 35 {
		t.Fatalf("mean = %+v", m)
	}
	if hr := b.All.Stats[HitRatio]; math.Abs(hr.Median-0.9) > 1e-9 {
		t.Fatalf("hit ratio = %+v", hr)
	}
	if len(b.ByHour) != 0 {
		t.Fatal("three days are not enough for hour-of-day profiles")
	}
}

func TestHourOfDayProfile(t *testing.T) {
	coverage := map[time.Time]float64{}
	var points []Point
	for d := 1; d <= 7; d++ {
		for h := 0; h < 24; h++ {
			coverage[hour(d, h)] = 3600
			ms := 3600.0 // 1 ms per call
			if h == 2 {
				ms = 36000 // the nightly batch hour is always slower
			}
			points = append(points, Point{At: hour(d, h), Calls: 3600, TotalMS: ms})
		}
	}
	b := Build(points, coverage, nil, hour(1, 0), hour(8, 0), 3600, 10)
	night, basis := b.For(hour(8, 2))
	if basis != BasisHourOfDay || night.Stats[MeanMS].Median != 10 {
		t.Fatalf("night: %v %+v", basis, night.Stats[MeanMS])
	}
	if day, _ := b.For(hour(8, 14)); day.Stats[MeanMS].Median != 1 {
		t.Fatalf("day: %+v", day.Stats[MeanMS])
	}
	// Without a profile for the hour, all hours are used.
	b.ByHour = nil
	if _, basis := b.For(hour(8, 2)); basis != BasisAllHours {
		t.Fatalf("basis = %v", basis)
	}
}
