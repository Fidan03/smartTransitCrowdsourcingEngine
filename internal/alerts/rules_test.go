package alerts

import (
	"testing"
	"time"

	"smart-transit/internal/crowd"
)

var t0 = time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { t := t0.Add(d); return &t }

// simulate runs Decide every 15 s over a status timeline, feeding back state,
// and returns the offset at which an alert would be created (or -1).
func simulate(p Policy, statusAt func(time.Duration) crowd.Status, until time.Duration) time.Duration {
	var prev Prev
	for off := time.Duration(0); off <= until; off += 15 * time.Second {
		now := t0.Add(off)
		cur := statusAt(off)
		d := p.Decide(prev, cur, now, false)
		if d.CreateAlert {
			return off
		}
		prev = Prev{Status: cur, HighSince: d.HighSince, CalculatedAt: &now}
	}
	return -1
}

func TestFirstHighStartsTimer(t *testing.T) {
	d := DefaultPolicy.Decide(Prev{}, crowd.StatusHigh, t0, false)
	if d.HighSince == nil || !d.HighSince.Equal(t0) || d.CreateAlert {
		t.Fatalf("got %+v", d)
	}
}

func TestAlertExactlyAtTenMinutesNotBefore(t *testing.T) {
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(10*time.Minute - 15*time.Second)}

	if d := DefaultPolicy.Decide(prev, crowd.StatusHigh, t0.Add(10*time.Minute-time.Second), false); d.CreateAlert {
		t.Fatal("alert created before 10 minutes")
	}
	d := DefaultPolicy.Decide(prev, crowd.StatusHigh, t0.Add(10*time.Minute), false)
	if !d.CreateAlert || !d.HighSince.Equal(t0) {
		t.Fatalf("no alert at exactly 10 minutes: %+v", d)
	}
}

func TestNoDuplicateWhenAlertOpen(t *testing.T) {
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(20 * time.Minute)}
	d := DefaultPolicy.Decide(prev, crowd.StatusHigh, t0.Add(20*time.Minute+15*time.Second), true)
	if d.CreateAlert || !d.ClearRecovered {
		t.Fatalf("got %+v", d)
	}
}

func TestReliableRecoveryResetsTimerAndMarksAlert(t *testing.T) {
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(12 * time.Minute)}
	for _, s := range []crowd.Status{crowd.StatusLow, crowd.StatusMedium} {
		d := DefaultPolicy.Decide(prev, s, t0.Add(12*time.Minute+15*time.Second), true)
		if d.HighSince != nil || !d.MarkRecovered || d.CreateAlert {
			t.Errorf("%s: got %+v", s, d)
		}
		if d := DefaultPolicy.Decide(prev, s, t0.Add(12*time.Minute+15*time.Second), false); d.MarkRecovered {
			t.Errorf("%s: nothing to mark without open alert", s)
		}
	}
}

func TestUnknownInvalidatesTimerWithoutClaimingRecovery(t *testing.T) {
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(5 * time.Minute)}
	d := DefaultPolicy.Decide(prev, crowd.StatusUnknown, t0.Add(5*time.Minute+15*time.Second), true)
	if d.HighSince != nil || d.MarkRecovered || d.CreateAlert {
		t.Fatalf("got %+v", d)
	}
}

func TestEvaluationGapRestartsTimer(t *testing.T) {
	// Last evaluation 5 minutes ago (> MaxGap 2m): continuity unproven.
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(9 * time.Minute)}
	now := t0.Add(14 * time.Minute)
	d := DefaultPolicy.Decide(prev, crowd.StatusHigh, now, false)
	if d.CreateAlert || !d.HighSince.Equal(now) {
		t.Fatalf("got %+v", d)
	}
}

func TestShortRestartKeepsTimer(t *testing.T) {
	// 60 s gap (restart) is within MaxGap: timer preserved, alert fires on time.
	prev := Prev{Status: crowd.StatusHigh, HighSince: at(0), CalculatedAt: at(9 * time.Minute)}
	d := DefaultPolicy.Decide(prev, crowd.StatusHigh, t0.Add(10*time.Minute), false)
	if !d.CreateAlert || !d.HighSince.Equal(t0) {
		t.Fatalf("got %+v", d)
	}
}

func TestTimelines(t *testing.T) {
	high := func(time.Duration) crowd.Status { return crowd.StatusHigh }
	if got := simulate(DefaultPolicy, high, 30*time.Minute); got != 10*time.Minute {
		t.Errorf("continuous HIGH: alert at %v, want 10m", got)
	}

	dip := func(off time.Duration) crowd.Status {
		if off == 6*time.Minute {
			return crowd.StatusMedium // one reliable MEDIUM resets the timer
		}
		return crowd.StatusHigh
	}
	if got := simulate(DefaultPolicy, dip, 30*time.Minute); got != 6*time.Minute+15*time.Second+10*time.Minute {
		t.Errorf("HIGH with MEDIUM dip: alert at %v, want 16m15s", got)
	}

	gap := func(off time.Duration) crowd.Status {
		if off >= 4*time.Minute && off < 5*time.Minute {
			return crowd.StatusUnknown
		}
		return crowd.StatusHigh
	}
	if got := simulate(DefaultPolicy, gap, 30*time.Minute); got != 15*time.Minute {
		t.Errorf("HIGH with UNKNOWN gap: alert at %v, want 15m", got)
	}

	flapping := func(off time.Duration) crowd.Status {
		if (off/time.Minute)%2 == 0 {
			return crowd.StatusHigh
		}
		return crowd.StatusLow
	}
	if got := simulate(DefaultPolicy, flapping, 60*time.Minute); got != -1 {
		t.Errorf("flapping status should never alert, got %v", got)
	}
}
