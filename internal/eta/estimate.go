// Package eta computes baseline estimated arrival times (plan section 6):
//
//	ETA = distance_remaining / estimated_speed + expected_stop_delay
//
// It deliberately refuses to give a precise ETA when GPS is stale, the bus is
// off its route, or the direction is unknown (plan 6, step 7; AC-05).
package eta

import (
	"math"
	"time"

	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

type Status string

const (
	Estimated   Status = "ESTIMATED"
	Unavailable Status = "UNAVAILABLE"
	Passed      Status = "PASSED" // bus is already beyond the stop (not returned to clients)
)

type Confidence string

const (
	High Confidence = "HIGH" // measured recent speed and fresh GPS
	Low  Confidence = "LOW"  // baseline speed or ageing GPS
	None Confidence = "NONE" // no estimate
)

type Config struct {
	BaselineSpeedKmh float64 // used when no reliable measured speed
	MinSpeedKmh      float64 // measured speeds outside [Min, Max] are ignored
	MaxSpeedKmh      float64
	DwellPerStop     time.Duration // allowance for each intermediate stop
	SpeedWindow      time.Duration // history used to measure recent speed
	MinSpeedSample   time.Duration // minimum history span for a measured speed
	MaxOffRouteM     float64       // farther than this from the route = off route
	PassedTolM       float64       // tolerance before a stop counts as passed
	AtStopM          float64       // within this distance = arriving now
	LowConfAge       time.Duration // GPS older than this lowers confidence
}

func DefaultConfig() Config {
	return Config{
		BaselineSpeedKmh: 20,
		MinSpeedKmh:      5,
		MaxSpeedKmh:      60,
		DwellPerStop:     20 * time.Second,
		SpeedWindow:      5 * time.Minute,
		MinSpeedSample:   60 * time.Second,
		MaxOffRouteM:     150,
		PassedTolM:       30,
		AtStopM:          40,
		LowConfAge:       30 * time.Second,
	}
}

// Input describes one vehicle relative to one target stop on a path.
type Input struct {
	Path             transit.Path
	TargetIdx        int     // index of the target stop in Path.Stops
	AlongM           float64 // vehicle's projected distance along the path
	OffsetM          float64 // vehicle's distance from the path
	Freshness        gps.Freshness
	GPSAge           time.Duration
	MeasuredSpeedKmh *float64 // recent along-route average speed, if measurable
}

type Result struct {
	Status     Status
	ETA        time.Duration // from now; valid only when Status == Estimated
	Confidence Confidence
	Reason     string // machine-readable explanation
	DistanceM  float64
	StopsAway  int
}

// Estimate is a pure function so the rules are unit-testable.
func Estimate(in Input, cfg Config) Result {
	switch in.Freshness {
	case gps.NoData:
		return Result{Status: Unavailable, Confidence: None, Reason: "no_gps"}
	case gps.Stale:
		return Result{Status: Unavailable, Confidence: None, Reason: "gps_stale"}
	}
	if in.TargetIdx < 0 || in.TargetIdx >= len(in.Path.Stops) {
		return Result{Status: Unavailable, Confidence: None, Reason: "stop_not_on_path"}
	}
	if in.OffsetM > cfg.MaxOffRouteM {
		return Result{Status: Unavailable, Confidence: None, Reason: "off_route"}
	}

	target := in.Path.Stops[in.TargetIdx].DistM
	remaining := target - in.AlongM
	if remaining < -cfg.PassedTolM {
		return Result{Status: Passed, Confidence: None, Reason: "passed"}
	}
	remaining = math.Max(0, remaining)

	if remaining <= cfg.AtStopM {
		return Result{Status: Estimated, ETA: 0, Confidence: High, Reason: "at_stop", DistanceM: round1(remaining)}
	}

	intermediate := 0
	for i := 0; i < in.TargetIdx; i++ {
		if d := in.Path.Stops[i].DistM; d > in.AlongM+cfg.AtStopM && d < target-1 {
			intermediate++
		}
	}

	conf, reason := High, "measured_speed"
	speed := cfg.BaselineSpeedKmh
	if m := in.MeasuredSpeedKmh; m != nil && *m >= cfg.MinSpeedKmh && *m <= cfg.MaxSpeedKmh {
		speed = *m
	} else {
		conf, reason = Low, "baseline_speed"
	}
	if in.GPSAge > cfg.LowConfAge {
		conf = Low
	}

	// distance (m) / speed (m/s) = seconds; plus dwell; minus time since the fix.
	secs := remaining/(speed/3.6) + float64(intermediate)*cfg.DwellPerStop.Seconds() - in.GPSAge.Seconds()
	secs = math.Max(0, secs)

	return Result{
		Status:     Estimated,
		ETA:        time.Duration(math.Round(secs)) * time.Second,
		Confidence: conf,
		Reason:     reason,
		DistanceM:  round1(remaining),
		StopsAway:  intermediate + 1,
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
