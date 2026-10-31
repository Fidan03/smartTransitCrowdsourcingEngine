package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"smart-transit/internal/transit"
)

func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	mode := "LIVE"
	if h.deps.Simulated {
		mode = "SIMULATED"
	}
	writeData(w, http.StatusOK, map[string]string{"status": "ok", "data_mode": mode})
}

func (h *handlers) listRoutes(w http.ResponseWriter, r *http.Request) {
	routes, err := h.deps.Transit.ListRoutes(r.Context())
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if routes == nil {
		routes = []transit.Route{}
	}
	writeData(w, http.StatusOK, routes)
}

func (h *handlers) getRoute(w http.ResponseWriter, r *http.Request) {
	route, err := h.deps.Transit.GetRoute(r.Context(), r.PathValue("id"))
	if errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusOK, route)
}

func (h *handlers) listRouteStops(w http.ResponseWriter, r *http.Request) {
	direction := 0
	if v := r.URL.Query().Get("direction"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || (d != 0 && d != 1) {
			writeError(w, http.StatusBadRequest, "invalid_direction", "direction must be 0 or 1")
			return
		}
		direction = d
	}

	stops, err := h.deps.Transit.ListRouteStops(r.Context(), r.PathValue("id"), direction)
	if errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "route_not_found", "route not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if stops == nil {
		stops = []transit.RouteStop{}
	}
	writeData(w, http.StatusOK, stops)
}
