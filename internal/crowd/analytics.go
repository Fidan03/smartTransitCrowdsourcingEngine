// Package crowd handles passenger crowding reports and the rule that turns
// recent reports into a crowding status (plan section 7).
package crowd

import (
	"time"
)

// Level is what a passenger can report.
type Level string

const (
	Low    Level = "LOW"
	Medium Level = "MEDIUM"
	High   Level = "HIGH"
)

func ParseLevel(s string) (Level, bool) {
	switch l := Level(s); l {
	case Low, Medium, High:
		return l, true
	}
	return "", false
}

// Status is a calculated crowding state. UNKNOWN means "no reliable
// evidence", which is never the same as LOW (plan 7.4).
type Status string

const (
	StatusLow     Status = "LOW"
	StatusMedium  Status = "MEDIUM"
	StatusHigh    Status = "HIGH"
	StatusUnknown Status = "UNKNOWN"
)

// Report is one stored passenger submission.
type Report struct {
	ID        string
	RouteID   string
	StopID    *string
	VehicleID *string
	TripID    *string
	Level     Level
	CreatedAt time.Time // server receipt time; client timestamps are never trusted
	SourceKey string    // opaque, non-personal submitter key
}

// Rule holds the crowd-status thresholds. Defaults follow plan 7.2 and are
// prototype values to be tuned with real report patterns.
type Rule struct {
	Window     time.Duration // only reports newer than now-Window count
	MinReports int           // fewer valid reports => UNKNOWN
	Threshold  float64       // share of HIGH (or LOW) needed for HIGH (or LOW)
}

var DefaultRule = Rule{Window: 10 * time.Minute, MinReports: 3, Threshold: 0.6}

// Result is a calculated status plus the evidence behind it.
type Result struct {
	Status       Status
	ValidCount   int
	LowCount     int
	MediumCount  int
	HighCount    int
	LastReportAt *time.Time
}

// Calculate applies the rule to reports at time now:
//   - only reports in (now-Window, now] count;
//   - only the latest report per SourceKey counts (one voice per submitter);
//   - fewer than MinReports valid reports => UNKNOWN;
//   - >= Threshold HIGH => HIGH; >= Threshold LOW => LOW; otherwise MEDIUM.
func (r Rule) Calculate(reports []Report, now time.Time) Result {
	cutoff := now.Add(-r.Window)
	latest := map[string]Report{}
	for _, rep := range reports {
		if !rep.CreatedAt.After(cutoff) || rep.CreatedAt.After(now) {
			continue
		}
		if prev, ok := latest[rep.SourceKey]; !ok || rep.CreatedAt.After(prev.CreatedAt) {
			latest[rep.SourceKey] = rep
		}
	}

	var res Result
	for _, rep := range latest {
		switch rep.Level {
		case Low:
			res.LowCount++
		case Medium:
			res.MediumCount++
		case High:
			res.HighCount++
		default:
			continue
		}
		res.ValidCount++
		if res.LastReportAt == nil || rep.CreatedAt.After(*res.LastReportAt) {
			t := rep.CreatedAt
			res.LastReportAt = &t
		}
	}

	need := r.Threshold * float64(res.ValidCount)
	const eps = 1e-9
	switch {
	case res.ValidCount < r.MinReports:
		res.Status = StatusUnknown
	case float64(res.HighCount)+eps >= need:
		res.Status = StatusHigh
	case float64(res.LowCount)+eps >= need:
		res.Status = StatusLow
	default:
		res.Status = StatusMedium
	}
	return res
}
