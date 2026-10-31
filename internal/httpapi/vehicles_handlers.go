package httpapi

import (
	"errors"
	"math"
	"net/http"
	"time"

	"smart-transit/internal/gps"
	"smart-transit/internal/transit"
)

type vehicleResponse struct {
	VehicleID   string        `json:"vehicle_id"`
	Label       string        `json:"label"`
	TripID      *string       `json:"trip_id"`
	Direction   *int          `json:"direction"`
	Latitude    *float64      `json:"latitude"`
	Longitude   *float64      `json:"longitude"`
	SpeedKmh    *float64      `json:"speed_kmh"`
	ObservedAt  *time.Time    `json:"observed_at"`
	ReceivedAt  *time.Time    `json:"received_at"`
	AgeSeconds  *float64      `json:"age_seconds"`
	Freshness   gps.Freshness `json:"freshness"`
	Source      *string       `json:"source"`
	IsSimulated *bool         `json:"is_simulated"`
}

type routeVehiclesResponse struct {
	RouteID           string            `json:"route_id"`
	GeneratedAt       time.Time         `json:"generated_at"`
	StaleAfterSeconds float64           `json:"stale_after_seconds"`
	Vehicles          []vehicleResponse `json:"vehicles"`
}

func (h *handlers) listRouteVehicles(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("id")
	if _, err := h.deps.Transit.GetRoute(r.Context(), routeID); errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found", "route not found")
		return
	} else if err != nil {
		writeInternal(w, r, err)
		return
	}

	now := h.deps.Clock.Now()
	states, err := h.deps.Vehicles.ListRouteVehicles(r.Context(), routeID, now.Add(-h.deps.GPSMaxAge))
	if err != nil {
		writeInternal(w, r, err)
		return
	}

	resp := routeVehiclesResponse{
		RouteID:           routeID,
		GeneratedAt:       now,
		StaleAfterSeconds: h.deps.GPSStaleAfter.Seconds(),
		Vehicles:          make([]vehicleResponse, 0, len(states)),
	}
	for _, s := range states {
		v := vehicleResponse{
			VehicleID: s.VehicleID, Label: s.Label, TripID: s.TripID, Direction: s.Direction,
			Latitude: s.Latitude, Longitude: s.Longitude, SpeedKmh: s.SpeedKmh,
			ObservedAt: s.ObservedAt, ReceivedAt: s.ReceivedAt,
			Source: s.Source, IsSimulated: s.IsSimulated,
			Freshness: gps.Classify(s.ObservedAt, now, h.deps.GPSStaleAfter),
		}
		if s.ObservedAt != nil {
			age := math.Max(0, math.Round(now.Sub(*s.ObservedAt).Seconds()*10)/10)
			v.AgeSeconds = &age
		}
		resp.Vehicles = append(resp.Vehicles, v)
	}
	writeData(w, http.StatusOK, resp)
}

type injectPositionRequest struct {
	VehicleID  string     `json:"vehicle_id"`
	RouteID    string     `json:"route_id"`
	TripID     string     `json:"trip_id"`
	Latitude   *float64   `json:"latitude"`
	Longitude  *float64   `json:"longitude"`
	SpeedKmh   *float64   `json:"speed_kmh"`
	ObservedAt *time.Time `json:"observed_at"` // optional; defaults to server time
}

// devInjectPosition lets developers push a simulated position through the
// normal ingestion path. Registered only outside production.
func (h *handlers) devInjectPosition(w http.ResponseWriter, r *http.Request) {
	var req injectPositionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Latitude == nil || req.Longitude == nil {
		writeError(w, http.StatusBadRequest, "invalid_position", "latitude and longitude are required")
		return
	}
	observed := h.deps.Clock.Now()
	if req.ObservedAt != nil {
		observed = *req.ObservedAt
	}

	receivedAt, err := h.deps.Ingester.Ingest(r.Context(), gps.Position{
		VehicleID: req.VehicleID, RouteID: req.RouteID, TripID: req.TripID,
		Latitude: *req.Latitude, Longitude: *req.Longitude, SpeedKmh: req.SpeedKmh,
		ObservedAt: observed, Source: "dev-inject", IsSimulated: true,
	})
	var ve *gps.ValidationError
	switch {
	case errors.As(err, &ve):
		writeError(w, http.StatusBadRequest, "invalid_position", ve.Msg)
		return
	case errors.Is(err, gps.ErrUnknownReference):
		writeError(w, http.StatusUnprocessableEntity, "unknown_reference", "vehicle_id, route_id, or trip_id does not exist")
		return
	case err != nil:
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusAccepted, map[string]any{"accepted": true, "received_at": receivedAt})
}
