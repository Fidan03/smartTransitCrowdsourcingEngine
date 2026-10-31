package opendata

import (
	"math"
	"sort"

	"smart-transit/internal/geo"
)

// BuildConfig controls how raw open data becomes a route network.
type BuildConfig struct {
	SimplifyM    float64 // Douglas-Peucker tolerance for stored paths
	MaxStopDistM float64 // stop must be this close to the path to belong to it
	CenterlineM  float64 // within this distance, side of road is ignored
	LoopCloseM   float64 // start/end closer than this => path is a round trip
	MinStops     int     // per direction; routes with fewer are skipped
	MinStopGapM  float64 // consecutive stops closer than this are merged (keep first)
}

func DefaultBuildConfig() BuildConfig {
	return BuildConfig{
		SimplifyM:    4,
		MaxStopDistM: 25,
		CenterlineM:  4,
		LoopCloseM:   1500,
		MinStops:     3,
		MinStopGapM:  15,
	}
}

// RouteStopRef is a stop placed on a route direction.
type RouteStopRef struct {
	Code  string
	DistM float64
}

// BuiltRoute is a route ready to load: two directional paths with ordered stops.
type BuiltRoute struct {
	Number   string
	Outbound []geo.Point // direction 0
	Return   []geo.Point // direction 1
	Stops0   []RouteStopRef
	Stops1   []RouteStopRef
	IsLoop   bool // geometry was a round trip split at the far terminal
}

// SkippedRoute explains why a route was not built.
type SkippedRoute struct {
	Number string
	Reason string
}

// Build turns route lines and stops into directional routes.
//
// Assumptions (documented for users of the data):
//   - A route line that starts and ends near the same place is a round trip;
//     it is split at the point farthest from the start (the far terminal).
//     Otherwise the line is direction 0 and direction 1 is its reverse.
//   - Lines are digitized in the direction of travel. Azerbaijan drives on the
//     right, so a stop belongs to a direction if it lies on the right-hand
//     side of the path (or practically on the centreline).
//   - Stop membership is inferred spatially (within MaxStopDistM); the source
//     stop file has no route column.
func Build(lines []RouteLine, stops []Stop, cfg BuildConfig) ([]BuiltRoute, []SkippedRoute) {
	var out []BuiltRoute
	var skipped []SkippedRoute
	for _, rl := range lines {
		path := Stitch(rl.Parts)
		if len(path) < 2 {
			skipped = append(skipped, SkippedRoute{rl.Number, "empty geometry"})
			continue
		}
		outbound, ret, loop := SplitDirections(path, cfg.LoopCloseM)
		outbound = geo.Simplify(outbound, cfg.SimplifyM)
		ret = geo.Simplify(ret, cfg.SimplifyM)

		s0 := MatchStops(outbound, stops, cfg)
		s1 := MatchStops(ret, stops, cfg)
		if len(s0) < cfg.MinStops || len(s1) < cfg.MinStops {
			skipped = append(skipped, SkippedRoute{rl.Number, "too few matched stops"})
			continue
		}
		out = append(out, BuiltRoute{Number: rl.Number, Outbound: outbound, Return: ret, Stops0: s0, Stops1: s1, IsLoop: loop})
	}
	return out, skipped
}

// Stitch joins line pieces into one path, greedily attaching the piece whose
// nearest endpoint is closest to the current path end (reversing it if needed).
func Stitch(parts [][]geo.Point) []geo.Point {
	if len(parts) == 0 {
		return nil
	}
	// Start from the longest piece.
	longest := 0
	for i, p := range parts {
		if len(p) > len(parts[longest]) {
			longest = i
		}
	}
	used := make([]bool, len(parts))
	used[longest] = true
	path := append([]geo.Point(nil), parts[longest]...)

	for remaining := len(parts) - 1; remaining > 0; remaining-- {
		bestI, bestD := -1, math.Inf(1)
		var atEnd, rev bool
		head, tail := path[0], path[len(path)-1]
		for i, p := range parts {
			if used[i] {
				continue
			}
			first, last := p[0], p[len(p)-1]
			for _, c := range []struct {
				d          float64
				atEnd, rev bool
			}{
				{geo.DistanceM(tail, first), true, false},
				{geo.DistanceM(tail, last), true, true},
				{geo.DistanceM(head, last), false, false},
				{geo.DistanceM(head, first), false, true},
			} {
				if c.d < bestD {
					bestI, bestD, atEnd, rev = i, c.d, c.atEnd, c.rev
				}
			}
		}
		used[bestI] = true
		piece := append([]geo.Point(nil), parts[bestI]...)
		if rev {
			reversePts(piece)
		}
		if atEnd {
			path = append(path, piece...)
		} else {
			path = append(piece, path...)
		}
	}
	return path
}

// SplitDirections splits a round-trip path at its far terminal. A path whose
// ends are farther apart than loopCloseM is treated as one-way geometry, and
// the return direction is its reverse.
func SplitDirections(path []geo.Point, loopCloseM float64) (outbound, ret []geo.Point, loop bool) {
	start := path[0]
	far, farD := 0, -1.0
	for i, p := range path {
		if d := geo.DistanceM(start, p); d > farD {
			far, farD = i, d
		}
	}
	closes := geo.DistanceM(start, path[len(path)-1]) <= loopCloseM
	if closes && far > 0 && far < len(path)-1 {
		outbound = append([]geo.Point(nil), path[:far+1]...)
		ret = append([]geo.Point(nil), path[far:]...)
		return outbound, ret, true
	}
	outbound = append([]geo.Point(nil), path...)
	ret = append([]geo.Point(nil), path...)
	reversePts(ret)
	return outbound, ret, false
}

// MatchStops finds stops served by a directional path, ordered along it.
func MatchStops(path []geo.Point, stops []Stop, cfg BuildConfig) []RouteStopRef {
	line, err := geo.NewPolyline(path)
	if err != nil {
		return nil
	}
	minLat, maxLat, minLon, maxLon := bbox(path)
	padLat := cfg.MaxStopDistM / 111000
	padLon := cfg.MaxStopDistM / (111000 * math.Cos(minLat*math.Pi/180))

	var refs []RouteStopRef
	for _, s := range stops {
		if s.Lat < minLat-padLat || s.Lat > maxLat+padLat || s.Lon < minLon-padLon || s.Lon > maxLon+padLon {
			continue
		}
		along, off, side := line.ProjectSide(geo.Point{Lat: s.Lat, Lon: s.Lon})
		if off > cfg.MaxStopDistM {
			continue
		}
		if off > cfg.CenterlineM && side < 0 { // left-hand side: other direction's stop
			continue
		}
		refs = append(refs, RouteStopRef{Code: s.Code, DistM: along})
	}
	sort.SliceStable(refs, func(i, j int) bool { return refs[i].DistM < refs[j].DistM })

	// Merge near-duplicates (paired poles/shelters at one stop).
	out := refs[:0]
	for _, r := range refs {
		if len(out) > 0 && r.DistM-out[len(out)-1].DistM < cfg.MinStopGapM {
			continue
		}
		out = append(out, r)
	}
	return out
}

func bbox(pts []geo.Point) (minLat, maxLat, minLon, maxLon float64) {
	minLat, maxLat, minLon, maxLon = 90, -90, 180, -180
	for _, p := range pts {
		minLat, maxLat = math.Min(minLat, p.Lat), math.Max(maxLat, p.Lat)
		minLon, maxLon = math.Min(minLon, p.Lon), math.Max(maxLon, p.Lon)
	}
	return
}

func reversePts(p []geo.Point) {
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
}
