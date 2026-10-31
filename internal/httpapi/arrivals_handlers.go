package httpapi

import (
	"errors"
	"net/http"
	"time"

	"smart-transit/internal/eta"
	"smart-transit/internal/transit"
)

type arrivalsResponse struct {
	Stop        transit.Stop  `json:"stop"`
	GeneratedAt time.Time     `json:"generated_at"`
	Arrivals    []eta.Arrival `json:"arrivals"`
}

// stopArrivals implements GET /api/v1/stops/{id}/arrivals[?route_id=].
func (h *handlers) stopArrivals(w http.ResponseWriter, r *http.Request) {
	stop, err := h.deps.Transit.GetStop(r.Context(), r.PathValue("id"))
	if errors.Is(err, transit.ErrNotFound) {
		writeError(w, http.StatusNotFound, "stop_not_found", "stop not found")
		return
	}
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	routes, err := h.deps.Transit.ListStopRoutes(r.Context(), stop.ID)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	if want := r.URL.Query().Get("route_id"); want != "" {
		filtered := routes[:0]
		for _, sr := range routes {
			if sr.RouteID == want {
				filtered = append(filtered, sr)
			}
		}
		routes = filtered
	}
	arrivals, err := h.deps.Arrivals.ArrivalsForStop(r.Context(), stop.ID, routes)
	if err != nil {
		writeInternal(w, r, err)
		return
	}
	writeData(w, http.StatusOK, arrivalsResponse{Stop: stop, GeneratedAt: h.deps.Clock.Now(), Arrivals: arrivals})
}
