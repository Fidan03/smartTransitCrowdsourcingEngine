// Package alerts implements the sustained-HIGH alert engine (plan section 8)
// and the operator alert lifecycle.
package alerts

import (
	"fmt"
	"time"

	"smart-transit/internal/crowd"
)

const (
	Reason         = "HIGH crowding persisted for at least 10 minutes"
	Recommendation = "Evaluate allocating a spare/additional bus"
)

// ReasonFor states the sustain time actually applied, so a shortened demo
// configuration never claims "10 minutes". Production is always 10 minutes.
func ReasonFor(sustain time.Duration) string {
	if sustain == 10*time.Minute {
		return Reason
	}
	return fmt.Sprintf("HIGH crowding persisted for at least %s (demo setting)", sustain)
}

// Policy is the continuity policy for sustained HIGH crowding.
//
//   - Reliable HIGH: start the timer (high_since) if not running; once it has
//     run for >= Sustain and no alert is open, create one alert.
//   - Reliable LOW/MEDIUM: reset the timer. An open alert is marked
//     "recovered" for operator review; it is never auto-resolved.
//   - UNKNOWN (insufficient evidence): invalidate the timer, because HIGH can
//     no longer be shown to be continuous, but do NOT claim recovery
//     (plan 8.1, 8.4).
//   - Evaluation gap: if the previous evaluation is older than MaxGap (e.g.
//     the backend was down), continuity cannot be proven, so the timer
//     restarts. Normal restarts are far shorter than MaxGap, so the persisted
//     timer survives them (AC-12).
type Policy struct {
	Sustain time.Duration
	MaxGap  time.Duration
}

var DefaultPolicy = Policy{Sustain: 10 * time.Minute, MaxGap: 2 * time.Minute}

// Prev is the persisted state from the previous evaluation.
type Prev struct {
	Status       crowd.Status
	HighSince    *time.Time
	CalculatedAt *time.Time // nil if never evaluated
}

type Decision struct {
	HighSince      *time.Time // new persisted timer value (nil = not running)
	CreateAlert    bool
	MarkRecovered  bool // open alert: crowding reliably recovered
	ClearRecovered bool // open alert: HIGH again after a recovery
}

// Decide is pure so timer boundaries and policies are unit-testable.
func (p Policy) Decide(prev Prev, cur crowd.Status, now time.Time, hasOpenAlert bool) Decision {
	var d Decision
	switch cur {
	case crowd.StatusHigh:
		gapBroken := prev.CalculatedAt == nil || now.Sub(*prev.CalculatedAt) > p.MaxGap
		hs := prev.HighSince
		if hs == nil || gapBroken || prev.Status != crowd.StatusHigh {
			t := now
			hs = &t
		}
		d.HighSince = hs
		if hasOpenAlert {
			d.ClearRecovered = true
		} else if now.Sub(*hs) >= p.Sustain {
			d.CreateAlert = true
		}
	case crowd.StatusLow, crowd.StatusMedium:
		d.MarkRecovered = hasOpenAlert
	case crowd.StatusUnknown:
		// Timer invalidated (HighSince nil); no recovery claimed.
	}
	return d
}
