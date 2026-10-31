package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"smart-transit/internal/clock"
	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

type fakeVehicles struct {
	states   []gps.VehicleState
	gotSince time.Time
	gotRoute string
}

func (f *fakeVehicles) ListRouteVehicles(_ context.Context, routeID string, since time.Time) ([]gps.VehicleState, error) {
	f.gotRoute, f.gotSince = routeID, since
	return f.states, nil
}

type fakeIngester struct {
	got []gps.Position
	err error
}

func (f *fakeIngester) Ingest(_ context.Context, p gps.Position) (time.Time, error) {
	if f.err != nil {
		return time.Time{}, f.err
	}
	f.got = append(f.got, p)
	return testNow, nil
}

func ptr[T any](v T) *T { return &v }

func vehicleRouter(fv *fakeVehicles, fi *fakeIngester, dev bool) http.Handler {
	ft := &fakeTransit{routes: []transit.Route{{ID: "route_d1"}}}
	return NewRouter(Deps{
		Transit: ft, Vehicles: fv, Ingester: fi, Clock: clock.NewFake(testNow),
		Simulated: true, GPSStaleAfter: time.Minute, GPSMaxAge: 15 * time.Minute, DevEndpoints: dev,
	})
}

func TestRouteVehiclesFreshness(t *testing.T) {
	fv := &fakeVehicles{states: []gps.VehicleState{
		{VehicleID: "fresh", Latitude: ptr(40.4), Longitude: ptr(49.8), ObservedAt: ptr(testNow.Add(-10 * time.Second)), IsSimulated: ptr(true)},
		{VehicleID: "stale", Latitude: ptr(40.4), Longitude: ptr(49.8), ObservedAt: ptr(testNow.Add(-5 * time.Minute)), IsSimulated: ptr(true)},
		{VehicleID: "nodata", TripID: ptr("t1")},
	}}
	rec := do(t, vehicleRouter(fv, &fakeIngester{}, false), http.MethodGet, "/api/v1/routes/route_d1/vehicles")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if !fv.gotSince.Equal(testNow.Add(-15*time.Minute)) || fv.gotRoute != "route_d1" {
		t.Errorf("store queried with route=%q since=%v", fv.gotRoute, fv.gotSince)
	}

	var body struct{ Data routeVehiclesResponse }
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]gps.Freshness{"fresh": gps.Fresh, "stale": gps.Stale, "nodata": gps.NoData}
	for _, v := range body.Data.Vehicles {
		if v.Freshness != want[v.VehicleID] {
			t.Errorf("%s freshness = %s, want %s", v.VehicleID, v.Freshness, want[v.VehicleID])
		}
	}
	if v := body.Data.Vehicles[1]; v.AgeSeconds == nil || *v.AgeSeconds != 300 {
		t.Errorf("stale age_seconds = %v, want 300", v.AgeSeconds)
	}
	if v := body.Data.Vehicles[2]; v.Latitude != nil || v.AgeSeconds != nil {
		t.Errorf("no-data vehicle must not have a position: %+v", v)
	}
}

func TestRouteVehiclesUnknownRoute(t *testing.T) {
	rec := do(t, vehicleRouter(&fakeVehicles{}, &fakeIngester{}, false), http.MethodGet, "/api/v1/routes/nope/vehicles")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func post(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	h.ServeHTTP(rec, req)
	return rec
}

const injectPath = "/api/v1/dev/simulator/positions"

func TestDevEndpointAbsentUnlessEnabled(t *testing.T) {
	rec := post(t, vehicleRouter(&fakeVehicles{}, &fakeIngester{}, false), injectPath, `{}`)
	if rec.Code == http.StatusAccepted || rec.Code == http.StatusBadRequest {
		t.Fatalf("dev endpoint must not be registered; got %d", rec.Code)
	}
}

func TestDevInjectPosition(t *testing.T) {
	fi := &fakeIngester{}
	rec := post(t, vehicleRouter(&fakeVehicles{}, fi, true), injectPath,
		`{"vehicle_id":"bus_101","route_id":"route_d1","latitude":40.4,"longitude":49.85,"speed_kmh":20}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	if len(fi.got) != 1 {
		t.Fatal("position not ingested")
	}
	p := fi.got[0]
	if p.Source != "dev-inject" || !p.IsSimulated || !p.ObservedAt.Equal(testNow) {
		t.Errorf("unexpected ingested position: %+v", p)
	}
}

func TestDevInjectErrors(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		ingErr error
		status int
	}{
		{"malformed json", `{`, nil, http.StatusBadRequest},
		{"unknown field", `{"vehicle_id":"b","latitude":1,"longitude":1,"evil":1}`, nil, http.StatusBadRequest},
		{"missing coords", `{"vehicle_id":"b"}`, nil, http.StatusBadRequest},
		{"validation", `{"vehicle_id":"b","latitude":1,"longitude":1}`, &gps.ValidationError{Msg: "x"}, http.StatusBadRequest},
		{"unknown vehicle", `{"vehicle_id":"b","latitude":1,"longitude":1}`, gps.ErrUnknownReference, http.StatusUnprocessableEntity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := post(t, vehicleRouter(&fakeVehicles{}, &fakeIngester{err: c.ingErr}, true), injectPath, c.body)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.status, rec.Body)
			}
		})
	}
}
