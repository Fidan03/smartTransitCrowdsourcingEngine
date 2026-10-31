package transit

import "testing"

func TestBuildPathDirections(t *testing.T) {
	out := []LatLon{{40.00, 49.00}, {40.01, 49.00}}
	ret := []LatLon{{40.01, 49.00}, {40.01, 49.01}, {40.00, 49.01}, {40.00, 49.00}} // different streets back
	stops0 := []RouteStop{{Stop: Stop{ID: "a", Latitude: 40.00, Longitude: 49.00}}, {Stop: Stop{ID: "b", Latitude: 40.01, Longitude: 49.00}}}

	// No return geometry: direction 1 is direction 0 reversed.
	p, err := BuildPath(Route{ID: "r", Geometry: out}, stops0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if first := p.Line.PointAt(0); first.Lat != 40.01 || first.Lon != 49.00 {
		t.Errorf("reversed path should start at the far end, got %v", first)
	}

	// With return geometry: direction 1 follows it, not reversed.
	p, err = BuildPath(Route{ID: "r", Geometry: out, GeometryReturn: ret}, stops0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Line.Points()) != 4 || p.Line.Points()[1] != (struct{ Lat, Lon float64 }{40.01, 49.01}) {
		t.Errorf("direction 1 should use the return geometry, got %v", p.Line.Points())
	}
	// Return loop ~852 + 1112 + 852 m, vs 1112 m outbound.
	if l := p.Line.LengthM(); l < 2700 || l > 2900 {
		t.Errorf("return path length %.0f m, want ~2815 m", l)
	}

	// Direction 0 ignores the return geometry.
	p, _ = BuildPath(Route{ID: "r", Geometry: out, GeometryReturn: ret}, stops0, 0)
	if len(p.Line.Points()) != 2 || p.StopIndex("b") != 1 {
		t.Errorf("direction 0 wrong: %v", p.Line.Points())
	}
}
