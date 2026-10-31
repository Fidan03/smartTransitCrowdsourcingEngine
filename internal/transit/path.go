package transit

import (
	"context"
	"fmt"
	"sync"

	"smart-transit/internal/geo"
)

// PathStop is a stop positioned along a route path.
type PathStop struct {
	StopID string
	Name   string
	DistM  float64 // distance from the start of the path
}

// Path is a route traversed in one direction, with stops located along it.
// It is shared by the simulator and the ETA service.
type Path struct {
	RouteID   string
	Direction int
	Line      geo.Polyline
	Stops     []PathStop // in travel order; DistM is non-decreasing
}

// StopIndex returns the index of stopID in the path, or -1.
func (p Path) StopIndex(stopID string) int {
	for i, s := range p.Stops {
		if s.StopID == stopID {
			return i
		}
	}
	return -1
}

// BuildPath creates the path for one direction. Direction 1 is the route
// geometry reversed. Stops must be given in travel order.
func BuildPath(route Route, stops []RouteStop, direction int) (Path, error) {
	pts := make([]geo.Point, len(route.Geometry))
	for i, ll := range route.Geometry {
		pts[i] = geo.Point{Lat: ll[0], Lon: ll[1]}
	}
	line, err := geo.NewPolyline(pts)
	if err != nil {
		return Path{}, fmt.Errorf("route %s geometry: %w", route.ID, err)
	}
	if direction == 1 {
		line = line.Reversed()
	}
	p := Path{RouteID: route.ID, Direction: direction, Line: line, Stops: make([]PathStop, len(stops))}
	prev := 0.0
	for i, st := range stops {
		d, _ := line.Project(geo.Point{Lat: st.Latitude, Lon: st.Longitude})
		if d < prev { // keep monotonic even if geometry doubles back
			d = prev
		}
		p.Stops[i] = PathStop{StopID: st.ID, Name: st.Name, DistM: d}
		prev = d
	}
	return p, nil
}

// PathSource is what PathCache needs to build paths.
type PathSource interface {
	GetRoute(ctx context.Context, id string) (Route, error)
	ListRouteStops(ctx context.Context, routeID string, direction int) ([]RouteStop, error)
}

// PathCache builds paths on demand and caches them. Route geometry is static
// reference data, so entries never expire during a process lifetime.
type PathCache struct {
	src PathSource
	mu  sync.Mutex
	m   map[string]Path
}

func NewPathCache(src PathSource) *PathCache {
	return &PathCache{src: src, m: map[string]Path{}}
}

func (c *PathCache) Get(ctx context.Context, routeID string, direction int) (Path, error) {
	key := fmt.Sprintf("%s/%d", routeID, direction)
	c.mu.Lock()
	p, ok := c.m[key]
	c.mu.Unlock()
	if ok {
		return p, nil
	}
	route, err := c.src.GetRoute(ctx, routeID)
	if err != nil {
		return Path{}, err
	}
	stops, err := c.src.ListRouteStops(ctx, routeID, direction)
	if err != nil {
		return Path{}, err
	}
	p, err = BuildPath(route, stops, direction)
	if err != nil {
		return Path{}, err
	}
	c.mu.Lock()
	c.m[key] = p
	c.mu.Unlock()
	return p, nil
}
