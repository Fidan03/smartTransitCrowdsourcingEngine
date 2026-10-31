// Package demand estimates EXPECTED crowding from historical ridership when
// there are not enough live passenger reports.
//
// It is an estimate, never evidence:
//   - it is never stored as a crowd report or crowd status;
//   - the alert engine does not use it (alerts need live reports, plan 8);
//   - it is always labelled as an estimate in the API and UI.
//
// Inputs: real BakuBus daily trips per route (opendata.az), averaged per
// weekday. Daily totals are spread over the day with an ASSUMED typical urban
// hourly profile, because the open data contains no hourly breakdown.
package demand

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Level string

const (
	Low    Level = "LOW"
	Medium Level = "MEDIUM"
	High   Level = "HIGH"
)

// Baku is UTC+4 all year (Azerbaijan abolished daylight saving in 2016).
var Baku = time.FixedZone("Asia/Baku", 4*3600)

// Assumed share of daily boardings per local hour (0-23). Weekdays have
// morning and evening peaks; weekends are flatter and later. These are
// generic assumptions, not measured Baku data, and are labelled as such.
var (
	weekdayProfile = normalize([24]float64{0.2, 0.1, 0.05, 0.05, 0.1, 0.6, 2.5, 6.5, 8.5, 7.0, 5.5, 5.0,
		5.2, 5.4, 5.5, 5.8, 6.6, 8.2, 8.4, 6.5, 4.6, 3.3, 2.0, 1.0})
	weekendProfile = normalize([24]float64{0.3, 0.1, 0.05, 0.05, 0.1, 0.4, 1.2, 2.5, 4.0, 5.5, 6.5, 7.0,
		7.2, 7.2, 7.1, 7.0, 7.0, 7.0, 6.8, 6.2, 5.2, 4.0, 2.6, 1.4})
)

func normalize(p [24]float64) [24]float64 {
	sum := 0.0
	for _, v := range p {
		sum += v
	}
	for i := range p {
		p[i] /= sum
	}
	return p
}

func profileFor(wd time.Weekday) [24]float64 {
	if wd == time.Saturday || wd == time.Sunday {
		return weekendProfile
	}
	return weekdayProfile
}

// Row is one route's trips on one service day.
type Row struct {
	RouteNumber string
	Date        time.Time
	Trips       int
}

// Estimate is an expected (not observed) crowding level.
type Estimate struct {
	Level             Level  `json:"level"`
	IsEstimate        bool   `json:"is_estimate"` // always true
	ExpectedTripsHour int    `json:"expected_trips_this_hour"`
	TypicalDailyTrips int    `json:"typical_daily_trips"`
	Weekday           string `json:"weekday"`
	LocalHour         int    `json:"local_hour"`
	BasedOnDays       int    `json:"based_on_days"`
	DataFrom          string `json:"data_from"`
	DataTo            string `json:"data_to"`
	Basis             string `json:"basis"`
}

// Summary is historical ridership context for operators.
type Summary struct {
	AvgDailyTrips int    `json:"avg_daily_trips"`
	LastDayTrips  int    `json:"last_day_trips"`
	LastDay       string `json:"last_day"`
	Days          int    `json:"days"`
}

type routeStats struct {
	wdSum   [7]float64
	wdN     [7]int
	sum     float64
	n       int
	last    time.Time
	lastVal int
}

// Model holds per-route weekday averages and network-wide level cut-offs.
type Model struct {
	routes       map[string]*routeStats
	lowCut       float64 // expected hourly trips below this => LOW
	highCut      float64 // at or above this => HIGH
	from, to     time.Time
	lowQ, highQ  float64
	serviceHours [2]int
}

// Build computes the model. LOW/HIGH cut-offs are the 35th and 75th
// percentiles of expected hourly trips over all routes, weekdays and service
// hours (06:00-22:59), i.e. relative to typical demand across the network.
func Build(rows []Row) *Model {
	m := &Model{routes: map[string]*routeStats{}, lowQ: 0.35, highQ: 0.75, serviceHours: [2]int{6, 22}}
	for _, r := range rows {
		st, ok := m.routes[r.RouteNumber]
		if !ok {
			st = &routeStats{}
			m.routes[r.RouteNumber] = st
		}
		wd := r.Date.Weekday()
		st.wdSum[wd] += float64(r.Trips)
		st.wdN[wd]++
		st.sum += float64(r.Trips)
		st.n++
		if r.Date.After(st.last) {
			st.last, st.lastVal = r.Date, r.Trips
		}
		if m.from.IsZero() || r.Date.Before(m.from) {
			m.from = r.Date
		}
		if r.Date.After(m.to) {
			m.to = r.Date
		}
	}

	var samples []float64
	for _, st := range m.routes {
		for wd := time.Sunday; wd <= time.Saturday; wd++ {
			daily := st.daily(wd)
			prof := profileFor(wd)
			for h := m.serviceHours[0]; h <= m.serviceHours[1]; h++ {
				samples = append(samples, daily*prof[h])
			}
		}
	}
	sort.Float64s(samples)
	m.lowCut = quantile(samples, m.lowQ)
	m.highCut = quantile(samples, m.highQ)
	return m
}

func (st *routeStats) daily(wd time.Weekday) float64 {
	if st.wdN[wd] > 0 {
		return st.wdSum[wd] / float64(st.wdN[wd])
	}
	if st.n > 0 {
		return st.sum / float64(st.n)
	}
	return 0
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Round(q * float64(len(sorted)-1)))
	return sorted[i]
}

// Estimate returns the expected level for a route at time now, or false if
// there is no ridership data for the route.
func (m *Model) Estimate(routeNumber string, now time.Time) (Estimate, bool) {
	if m == nil {
		return Estimate{}, false
	}
	st, ok := m.routes[routeNumber]
	if !ok || st.n == 0 {
		return Estimate{}, false
	}
	local := now.In(Baku)
	wd, h := local.Weekday(), local.Hour()
	daily := st.daily(wd)
	expected := daily * profileFor(wd)[h]

	lvl := Medium
	switch {
	case expected < m.lowCut:
		lvl = Low
	case expected >= m.highCut:
		lvl = High
	}
	days := st.wdN[wd]
	if days == 0 {
		days = st.n
	}
	return Estimate{
		Level:             lvl,
		IsEstimate:        true,
		ExpectedTripsHour: int(math.Round(expected)),
		TypicalDailyTrips: int(math.Round(daily)),
		Weekday:           wd.String(),
		LocalHour:         h,
		BasedOnDays:       days,
		DataFrom:          m.from.Format("2006-01-02"),
		DataTo:            m.to.Format("2006-01-02"),
		Basis: fmt.Sprintf("Average %s ridership on this route (%d days of BakuBus data) "+
			"spread over the day with an assumed typical hourly pattern; compared with typical demand across the network.",
			wd, days),
	}, true
}

// Summary returns historical ridership context for a route.
func (m *Model) Summary(routeNumber string) (Summary, bool) {
	if m == nil {
		return Summary{}, false
	}
	st, ok := m.routes[routeNumber]
	if !ok || st.n == 0 {
		return Summary{}, false
	}
	return Summary{
		AvgDailyTrips: int(math.Round(st.sum / float64(st.n))),
		LastDayTrips:  st.lastVal,
		LastDay:       st.last.Format("2006-01-02"),
		Days:          st.n,
	}, true
}

// Service keeps a Model loaded from PostgreSQL and refreshes it periodically.
type Service struct {
	pool *pgxpool.Pool
	mu   sync.RWMutex
	m    *Model
}

func NewService(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

func (s *Service) Reload(ctx context.Context) error {
	rows, err := s.pool.Query(ctx, `SELECT route_number, service_date, trips FROM route_daily_ridership`)
	if err != nil {
		return err
	}
	data, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Row, error) {
		var x Row
		err := r.Scan(&x.RouteNumber, &x.Date, &x.Trips)
		return x, err
	})
	if err != nil {
		return err
	}
	m := Build(data)
	s.mu.Lock()
	s.m = m
	s.mu.Unlock()
	return nil
}

func (s *Service) model() *Model {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.m
}

func (s *Service) Estimate(routeNumber string, now time.Time) (Estimate, bool) {
	return s.model().Estimate(routeNumber, now)
}

func (s *Service) Summary(routeNumber string) (Summary, bool) {
	return s.model().Summary(routeNumber)
}
