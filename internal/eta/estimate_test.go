package eta

import (
	"testing"
	"time"

	"smart-transit/internal/geo"
	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

// testPath: stops at 0 m, 1000 m, 2000 m, 3000 m along a straight line.
func testPath(t *testing.T) transit.Path {
	t.Helper()
	line, err := geo.NewPolyline([]geo.Point{{Lat: 40, Lon: 49}, {Lat: 40.05, Lon: 49}})
	if err != nil {
		t.Fatal(err)
	}
	return transit.Path{Line: line, Stops: []transit.PathStop{
		{StopID: "s0", DistM: 0}, {StopID: "s1", DistM: 1000}, {StopID: "s2", DistM: 2000}, {StopID: "s3", DistM: 3000},
	}}
}

func ptr[T any](v T) *T { return &v }

func base(t *testing.T) Input {
	return Input{Path: testPath(t), TargetIdx: 3, AlongM: 500, Freshness: gps.Fresh, GPSAge: 0}
}

func TestEstimateBaselineSpeedWithDwell(t *testing.T) {
	cfg := DefaultConfig() // baseline 20 km/h, 20 s dwell
	r := Estimate(base(t), cfg)
	// 2500 m at 20 km/h (5.556 m/s) = 450 s, plus 2 intermediate stops * 20 s = 490 s.
	if r.Status != Estimated || r.ETA != 490*time.Second {
		t.Fatalf("got %+v, want ESTIMATED 490s", r)
	}
	if r.Confidence != Low || r.Reason != "baseline_speed" || r.StopsAway != 3 || r.DistanceM != 2500 {
		t.Errorf("unexpected details: %+v", r)
	}
}

func TestEstimateMeasuredSpeedUnitsKmhToSeconds(t *testing.T) {
	in := base(t)
	in.MeasuredSpeedKmh = ptr(36.0) // 10 m/s
	in.TargetIdx = 1                // 500 m away, no intermediate stops
	r := Estimate(in, DefaultConfig())
	if r.ETA != 50*time.Second || r.Confidence != High || r.StopsAway != 1 {
		t.Fatalf("got %+v, want 50s HIGH", r)
	}
}

func TestEstimateIgnoresImplausibleMeasuredSpeed(t *testing.T) {
	for _, sp := range []float64{0, 2, 200} {
		in := base(t)
		in.MeasuredSpeedKmh = ptr(sp)
		if r := Estimate(in, DefaultConfig()); r.Reason != "baseline_speed" {
			t.Errorf("speed %.0f: reason %q, want baseline_speed", sp, r.Reason)
		}
	}
}

func TestEstimateSubtractsGPSAgeAndLowersConfidence(t *testing.T) {
	in := base(t)
	in.MeasuredSpeedKmh = ptr(36.0)
	in.TargetIdx = 1
	in.GPSAge = 40 * time.Second
	r := Estimate(in, DefaultConfig())
	if r.ETA != 10*time.Second || r.Confidence != Low {
		t.Fatalf("got %+v, want 10s LOW", r)
	}
}

func TestEstimateNeverNegative(t *testing.T) {
	in := base(t)
	in.MeasuredSpeedKmh = ptr(36.0)
	in.TargetIdx = 1
	in.GPSAge = 55 * time.Second
	if r := Estimate(in, DefaultConfig()); r.ETA != 0 {
		t.Fatalf("ETA = %v, want 0", r.ETA)
	}
}

func TestEstimateStaleAndNoDataAreUnavailable(t *testing.T) {
	for f, reason := range map[gps.Freshness]string{gps.Stale: "gps_stale", gps.NoData: "no_gps"} {
		in := base(t)
		in.Freshness = f
		r := Estimate(in, DefaultConfig())
		if r.Status != Unavailable || r.Reason != reason || r.ETA != 0 {
			t.Errorf("%s: got %+v", f, r)
		}
	}
}

func TestEstimateOffRoute(t *testing.T) {
	in := base(t)
	in.OffsetM = 500
	if r := Estimate(in, DefaultConfig()); r.Status != Unavailable || r.Reason != "off_route" {
		t.Fatalf("got %+v", r)
	}
}

func TestEstimatePassedAndAtStop(t *testing.T) {
	in := base(t)
	in.TargetIdx = 0 // stop at 0 m, bus at 500 m
	if r := Estimate(in, DefaultConfig()); r.Status != Passed {
		t.Errorf("want PASSED, got %+v", r)
	}
	in.TargetIdx = 1
	in.AlongM = 980
	if r := Estimate(in, DefaultConfig()); r.Status != Estimated || r.ETA != 0 || r.Reason != "at_stop" {
		t.Errorf("want at_stop, got %+v", r)
	}
	in.AlongM = 1020 // slightly past, within tolerance: still "at stop"
	if r := Estimate(in, DefaultConfig()); r.Reason != "at_stop" {
		t.Errorf("within passed tolerance should be at_stop, got %+v", r)
	}
}

func TestEstimateInvalidTarget(t *testing.T) {
	in := base(t)
	in.TargetIdx = 9
	if r := Estimate(in, DefaultConfig()); r.Status != Unavailable {
		t.Fatalf("got %+v", r)
	}
}
