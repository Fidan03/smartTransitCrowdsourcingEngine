package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"smart-transit/internal/crowd"
	"smart-transit/internal/demand"
	"smart-transit/internal/transit"
)

type crowdReportRequest struct {
	RouteID    string `json:"route_id"`
	StopID     string `json:"stop_id"`
	VehicleID  string `json:"vehicle_id"`
	CrowdLevel string `json:"crowd_level"`
}

type crowdReportResponse struct {
	ID         string    `json:"id"`
	Accepted   bool      `json:"accepted"`
	ReceivedAt time.Time `json:"received_at"`
}

// submitCrowdReport implements POST /api/v1/crowd-reports (plan 7.1).
// Optional header X-Client-Id: a random per-browser ID used (hashed) for
// duplicate control. The server assigns the ID and receipt timestamp.
func (h *handlers) submitCrowdReport(w http.ResponseWriter, r *http.Request) {
	var req crowdReportRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	key := crowd.SourceKey(h.deps.ReportKeySecret, r.Header.Get("X-Client-Id"), clientIP(r))
	rep, err := h.deps.Crowd.Submit(r.Context(), crowd.SubmitRequest{
		RouteID: req.RouteID, StopID: req.StopID, VehicleID: req.VehicleID, Level: req.CrowdLevel,
	}, key)

	var dup *crowd.DuplicateError
	switch {
	case errors.Is(err, crowd.ErrInvalidLevel):
		writeError(w, http.StatusBadRequest, "invalid_crowd_level", err.Error())
	case errors.Is(err, crowd.ErrUnknownRoute):
		writeError(w, http.StatusUnprocessableEntity, "unknown_route", err.Error())
	case errors.Is(err, crowd.ErrUnknownStop):
		writeError(w, http.StatusUnprocessableEntity, "unknown_stop", err.Error())
	case errors.Is(err, crowd.ErrUnknownVehicle):
		writeError(w, http.StatusUnprocessableEntity, "unknown_vehicle", err.Error())
	case errors.As(err, &dup):
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(dup.RetryAfter.Seconds()))))
		writeError(w, http.StatusTooManyRequests, "duplicate_report",
			"you already reported on this route recently; please wait before reporting again")
	case err != nil:
		writeInternal(w, r, err)
	default:
		writeData(w, http.StatusCreated, crowdReportResponse{ID: rep.ID, Accepted: true, ReceivedAt: rep.CreatedAt})
	}
}

type crowdStatusResponse struct {
	crowd.RouteStatus         // live status from passenger reports (drives alerts)
	WindowMinutes     float64 `json:"window_minutes"`
	MinReports        int     `json:"min_reports"`
	Threshold         float64 `json:"threshold"`
	// Expected is a display-only estimate from historical ridership, shown
	// especially when the live status is UNKNOWN. Null if no ridership data.
	Expected *demand.Estimate `json:"expected"`
}

// routeCrowdStatus returns the persisted status and its evidence (plan 7.4).
func (h *handlers) routeCrowdStatus(w http.ResponseWriter, r *http.Request) {
	routeID := r.PathValue("id")
	route, err := h.deps.Transit.GetRoute(r.Context(), routeID)
	if errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found", "route not found")
		return
	} else if err != nil {
		writeInternal(w, r, err)
		return
	}
	st, err := h.deps.CrowdRead.GetRouteStatus(r.Context(), routeID)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	resp := crowdStatusResponse{
		RouteStatus:   st,
		WindowMinutes: h.deps.CrowdRule.Window.Minutes(),
		MinReports:    h.deps.CrowdRule.MinReports,
		Threshold:     h.deps.CrowdRule.Threshold,
	}
	if h.deps.Demand != nil {
		if e, ok := h.deps.Demand.Estimate(route.RouteNumber, h.deps.Clock.Now()); ok {
			resp.Expected = &e
		}
	}
	writeData(w, http.StatusOK, resp)
}

func (h *handlers) listDataSources(w http.ResponseWriter, r *http.Request) {
	list := []transit.DataSource{}
	if h.deps.Sources != nil {
		var err error
		if list, err = h.deps.Sources.ListDataSources(r.Context()); err != nil {
			writeInternal(w, r, err)
			return
		}
		if list == nil {
			list = []transit.DataSource{}
		}
	}
	writeData(w, http.StatusOK, list)
}
