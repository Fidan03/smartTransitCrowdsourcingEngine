package crowd

import (
	"fmt"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

// reps builds one report per level from distinct sources, 1 minute old.
func reps(levels ...Level) []Report {
	out := make([]Report, len(levels))
	for i, l := range levels {
		out[i] = Report{Level: l, CreatedAt: now.Add(-time.Minute), SourceKey: fmt.Sprintf("src%d", i)}
	}
	return out
}

func TestPlanExamples(t *testing.T) {
	// The exact table from plan 7.2.
	cases := []struct {
		name   string
		in     []Report
		status Status
	}{
		{"1 HIGH", reps(High), StatusUnknown},
		{"3 HIGH", reps(High, High, High), StatusHigh},
		{"2 HIGH 1 MEDIUM", reps(High, High, Medium), StatusHigh},
		{"1 HIGH 1 MEDIUM 1 LOW", reps(High, Medium, Low), StatusMedium},
	}
	for _, c := range cases {
		if got := DefaultRule.Calculate(c.in, now); got.Status != c.status {
			t.Errorf("%s: got %s, want %s", c.name, got.Status, c.status)
		}
	}
}

func TestZeroOneTwoReportsAreUnknown(t *testing.T) {
	for _, in := range [][]Report{nil, reps(High), reps(High, High)} {
		got := DefaultRule.Calculate(in, now)
		if got.Status != StatusUnknown || got.ValidCount != len(in) {
			t.Errorf("%d reports: got %+v", len(in), got)
		}
	}
}

func TestThresholdBoundaries(t *testing.T) {
	// 3 of 5 = exactly 60% => HIGH; 2 of 4 = 50% => MEDIUM.
	if got := DefaultRule.Calculate(reps(High, High, High, Low, Medium), now); got.Status != StatusHigh {
		t.Errorf("60%% HIGH: got %s", got.Status)
	}
	if got := DefaultRule.Calculate(reps(High, High, Low, Low), now); got.Status != StatusMedium {
		t.Errorf("50/50: got %s", got.Status)
	}
	if got := DefaultRule.Calculate(reps(Low, Low, Low, High, Medium), now); got.Status != StatusLow {
		t.Errorf("60%% LOW: got %s", got.Status)
	}
	if got := DefaultRule.Calculate(reps(Medium, Medium, Medium), now); got.Status != StatusMedium {
		t.Errorf("all MEDIUM: got %s", got.Status)
	}
}

func TestWindowExcludesOldAndFutureReports(t *testing.T) {
	in := reps(High, High, High)
	in[0].CreatedAt = now.Add(-10 * time.Minute) // exactly at cutoff: excluded
	in[1].CreatedAt = now.Add(-11 * time.Minute)
	in[2].CreatedAt = now.Add(time.Minute) // future: excluded
	got := DefaultRule.Calculate(in, now)
	if got.ValidCount != 0 || got.Status != StatusUnknown {
		t.Fatalf("got %+v", got)
	}

	in = reps(High, High, High)
	in[0].CreatedAt = now.Add(-10*time.Minute + time.Second) // just inside
	if got := DefaultRule.Calculate(in, now); got.ValidCount != 3 {
		t.Fatalf("just-inside report not counted: %+v", got)
	}
}

func TestOneVoicePerSource(t *testing.T) {
	// One device spamming HIGH counts once; its latest report wins.
	in := []Report{
		{Level: High, SourceKey: "spammer", CreatedAt: now.Add(-5 * time.Minute)},
		{Level: High, SourceKey: "spammer", CreatedAt: now.Add(-4 * time.Minute)},
		{Level: Low, SourceKey: "spammer", CreatedAt: now.Add(-3 * time.Minute)},
		{Level: Low, SourceKey: "a", CreatedAt: now.Add(-time.Minute)},
		{Level: Medium, SourceKey: "b", CreatedAt: now.Add(-time.Minute)},
	}
	got := DefaultRule.Calculate(in, now)
	if got.ValidCount != 3 || got.LowCount != 2 || got.HighCount != 0 || got.Status != StatusLow {
		t.Fatalf("got %+v", got)
	}
}

func TestLastReportAt(t *testing.T) {
	in := reps(High, High, High)
	in[1].CreatedAt = now.Add(-10 * time.Second)
	got := DefaultRule.Calculate(in, now)
	if got.LastReportAt == nil || !got.LastReportAt.Equal(now.Add(-10*time.Second)) {
		t.Fatalf("LastReportAt = %v", got.LastReportAt)
	}
}

func TestParseLevel(t *testing.T) {
	for _, s := range []string{"LOW", "MEDIUM", "HIGH"} {
		if _, ok := ParseLevel(s); !ok {
			t.Errorf("%s rejected", s)
		}
	}
	for _, s := range []string{"", "low", "UNKNOWN", "VERY_HIGH"} {
		if _, ok := ParseLevel(s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}
