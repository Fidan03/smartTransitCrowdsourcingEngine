package demand

import (
	"math"
	"testing"
	"time"
)

func day(s string) time.Time { t, _ := time.Parse("2006-01-02", s); return t }

// rows: route "big" carries 10x route "small"; Mondays are busier on "big".
func testRows() []Row {
	var rows []Row
	start := day("2026-01-05") // a Monday
	for i := 0; i < 28; i++ {
		d := start.AddDate(0, 0, i)
		big := 20000
		if d.Weekday() == time.Monday {
			big = 24000
		}
		rows = append(rows, Row{"big", d, big}, Row{"small", d, 2000})
	}
	return rows
}

// at returns a time with the given Baku-local weekday date and hour.
func at(date string, hour int) time.Time {
	d := day(date)
	return time.Date(d.Year(), d.Month(), d.Day(), hour, 0, 0, 0, Baku)
}

func TestProfilesSumToOne(t *testing.T) {
	for _, p := range [][24]float64{weekdayProfile, weekendProfile} {
		s := 0.0
		for _, v := range p {
			s += v
		}
		if math.Abs(s-1) > 1e-9 {
			t.Fatalf("profile sums to %v", s)
		}
	}
}

func TestEstimateLevels(t *testing.T) {
	m := Build(testRows())

	// Busy route, Monday evening peak => HIGH.
	e, ok := m.Estimate("big", at("2026-02-02", 18))
	if !ok || e.Level != High || !e.IsEstimate {
		t.Fatalf("big Monday 18:00: %+v", e)
	}
	if e.TypicalDailyTrips != 24000 || e.Weekday != "Monday" || e.LocalHour != 18 || e.BasedOnDays != 4 {
		t.Errorf("details wrong: %+v", e)
	}
	// Small route at night => LOW.
	if e, _ := m.Estimate("small", at("2026-02-03", 3)); e.Level != Low {
		t.Errorf("small 03:00: %s", e.Level)
	}
	// Busy route late at night is not HIGH.
	if e, _ := m.Estimate("big", at("2026-02-03", 23)); e.Level == High {
		t.Errorf("big 23:00 should not be HIGH: %+v", e)
	}
	// Unknown route => no estimate (never a fabricated LOW).
	if _, ok := m.Estimate("nope", at("2026-02-03", 12)); ok {
		t.Error("unknown route should give no estimate")
	}
	var nilModel *Model
	if _, ok := nilModel.Estimate("big", time.Now()); ok {
		t.Error("nil model should give no estimate")
	}
}

func TestEstimateUsesBakuLocalTime(t *testing.T) {
	m := Build(testRows())
	// 14:00 UTC = 18:00 in Baku.
	utc := time.Date(2026, 2, 2, 14, 0, 0, 0, time.UTC)
	if e, _ := m.Estimate("big", utc); e.LocalHour != 18 || e.Weekday != "Monday" {
		t.Fatalf("got hour %d %s, want 18 Monday", e.LocalHour, e.Weekday)
	}
	// 21:30 UTC Sunday = 01:30 Monday in Baku.
	if e, _ := m.Estimate("big", time.Date(2026, 2, 1, 21, 30, 0, 0, time.UTC)); e.Weekday != "Monday" || e.LocalHour != 1 {
		t.Fatalf("day boundary: %s %d", e.Weekday, e.LocalHour)
	}
}

func TestWeekendUsesFlatterProfile(t *testing.T) {
	m := Build(testRows())
	wd, _ := m.Estimate("big", at("2026-02-03", 8)) // Tuesday morning peak
	we, _ := m.Estimate("big", at("2026-02-07", 8)) // Saturday morning
	if we.ExpectedTripsHour >= wd.ExpectedTripsHour {
		t.Errorf("Saturday 08:00 (%d) should be quieter than Tuesday 08:00 (%d)", we.ExpectedTripsHour, wd.ExpectedTripsHour)
	}
}

func TestSummary(t *testing.T) {
	m := Build(testRows())
	s, ok := m.Summary("big")
	// 4 Mondays x 24000 + 24 other days x 20000 = 576000 / 28 = 20571.
	if !ok || s.Days != 28 || s.LastDay != "2026-02-01" || s.LastDayTrips != 20000 || s.AvgDailyTrips != 20571 {
		t.Fatalf("summary: %+v", s)
	}
}
