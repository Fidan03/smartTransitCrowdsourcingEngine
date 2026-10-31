package opendata

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"smart-transit/internal/geo"
)

const sampleKML = `<?xml version="1.0" encoding="UTF-8"?>
<kml xmlns="http://www.opengis.net/kml/2.2"><Document><Folder>
  <Placemark><name>62</name><MultiGeometry>
    <LineString><coordinates> 49.80,40.40,0 49.81,40.40,0 </coordinates></LineString>
    <LineString><coordinates> 49.82,40.40,0 49.81,40.40,0 </coordinates></LineString>
  </MultiGeometry></Placemark>
  <Placemark><name> 7A </name><LineString><coordinates>49.9,40.4 49.91,40.41</coordinates></LineString></Placemark>
  <Placemark><name></name><LineString><coordinates>49.9,40.4 49.91,40.41</coordinates></LineString></Placemark>
</Folder></Document></kml>`

func p(lat, lon float64) geo.Point { return geo.Point{Lat: lat, Lon: lon} }

func TestParseKMLAndKMZ(t *testing.T) {
	lines, err := ParseKML([]byte(sampleKML))
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 2 || lines[0].Number != "62" || len(lines[0].Parts) != 2 || lines[1].Number != "7A" {
		t.Fatalf("unexpected lines: %+v", lines)
	}
	if p := lines[0].Parts[0][1]; p.Lat != 40.40 || p.Lon != 49.81 {
		t.Errorf("coordinates must be lon,lat: got %+v", p)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("doc.kml")
	w.Write([]byte(sampleKML))
	zw.Close()
	fromKMZ, err := ParseKMZ(buf.Bytes())
	if err != nil || len(fromKMZ) != 2 {
		t.Fatalf("kmz: %v %v", fromKMZ, err)
	}
	if _, err := ParseKMZ([]byte("not a zip")); err == nil {
		t.Error("expected error for invalid kmz")
	}
}

func TestParseStops(t *testing.T) {
	csv := "\xef\xbb\xbfID,AD,ID,LONGITUDE,LATITUDE,Tip\n" +
		"1,28 May NMM,001003598,49.848577,40.379488,28 may HUB pavilion\n" +
		"2,Planned stop,001009999,49.85,40.38,Təklif\n" + // proposed: skipped
		"3,Bad coords,001001111,0,0,Yol Nişanı\n" + // outside Azerbaijan: skipped
		"4,Duplicate,001003598,49.9,40.4,\n" + // duplicate code: skipped
		`5,"Küçə, giriş 3",001005146,49.416277,40.089006,Yol Nişanı` + "\n"
	stops, skipped, err := ParseStops(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(stops) != 2 || skipped != 3 {
		t.Fatalf("stops=%d skipped=%d: %+v", len(stops), skipped, stops)
	}
	if s := stops[0]; s.Code != "001003598" || s.Name != "28 May NMM" || s.Lat != 40.379488 || s.Lon != 49.848577 {
		t.Errorf("first stop: %+v", s)
	}
	if stops[1].Name != "Küçə, giriş 3" {
		t.Errorf("quoted name: %q", stops[1].Name)
	}
}

func TestParseRidership(t *testing.T) {
	csv := "\xef\xbb\xbfTarix (gün-ay-il),Marşrutun adı,Gündəlik gediş sayı\n" +
		"\"2026-01-01 00:00:00,000\",5,12030\n" +
		"\"2026-01-02 00:00:00,000\",7A,8012\n" +
		"garbage,x,y\n"
	rows, err := ParseRidership(strings.NewReader(csv))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].RouteNumber != "5" || rows[0].Trips != 12030 || rows[1].Date.Day() != 2 {
		t.Fatalf("rows: %+v", rows)
	}
	if _, err := ParseRidership(strings.NewReader("a,b,c\n")); err == nil {
		t.Error("expected error for empty data")
	}
}

func TestStitchReordersAndReversesParts(t *testing.T) {
	a := []geo.Point{p(40, 49.00), p(40, 49.01), p(40, 49.02)}
	b := []geo.Point{p(40, 49.04), p(40, 49.03)} // reversed, belongs after a
	c := []geo.Point{p(40, 48.98), p(40, 48.99)} // belongs before a
	got := Stitch([][]geo.Point{b, a, c})
	want := []float64{48.98, 48.99, 49.00, 49.01, 49.02, 49.03, 49.04}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, w := range want {
		if got[i].Lon != w {
			t.Fatalf("got %v", got)
		}
	}
}

func TestSplitDirections(t *testing.T) {
	// Round trip: out east 2 km, back on a parallel street.
	loop := []geo.Point{p(40, 49.00), p(40, 49.01), p(40, 49.02), p(40.001, 49.02), p(40.001, 49.01), p(40.001, 49.00)}
	out, ret, isLoop := SplitDirections(loop, 1500)
	if !isLoop || len(out) != 4 || len(ret) != 3 || out[len(out)-1] != ret[0] {
		t.Fatalf("loop split: out=%v ret=%v loop=%v", out, ret, isLoop)
	}

	// One-way geometry (ends far apart): return is the reverse.
	line := []geo.Point{p(40, 49.00), p(40, 49.05)}
	out, ret, isLoop = SplitDirections(line, 1500)
	if isLoop || out[0] != ret[1] || out[1] != ret[0] {
		t.Fatalf("one-way split: out=%v ret=%v", out, ret)
	}
}

func TestMatchStopsRightHandSideAndOrder(t *testing.T) {
	// Eastbound path along latitude 40.0; north side is the LEFT side.
	path := []geo.Point{p(40, 49.00), p(40, 49.03)}
	stops := []Stop{
		{Code: "far_right", Lat: 39.99985, Lon: 49.02},   // ~17 m south (right): included, 3rd
		{Code: "near_right", Lat: 39.99990, Lon: 49.005}, // ~11 m south: included, 1st
		{Code: "left", Lat: 40.00015, Lon: 49.01},        // ~17 m north (left): other direction
		{Code: "centre", Lat: 40.00002, Lon: 49.012},     // ~2 m north: on centreline, included, 2nd
		{Code: "too_far", Lat: 39.9995, Lon: 49.015},     // ~55 m: excluded
		{Code: "paired", Lat: 39.99990, Lon: 49.00505},   // 4 m after near_right: merged
		{Code: "elsewhere", Lat: 41, Lon: 49.01},         // far away: excluded by bbox
	}
	got := MatchStops(path, stops, DefaultBuildConfig())
	var codes []string
	for _, r := range got {
		codes = append(codes, r.Code)
	}
	if strings.Join(codes, ",") != "near_right,centre,far_right" {
		t.Fatalf("matched %v", codes)
	}
	for i := 1; i < len(got); i++ {
		if got[i].DistM <= got[i-1].DistM {
			t.Fatal("stops must be ordered along the path")
		}
	}

	// Travelling the other way, the north-side stop is now on the right.
	rev := []geo.Point{p(40, 49.03), p(40, 49.00)}
	back := MatchStops(rev, stops, DefaultBuildConfig())
	found := false
	for _, r := range back {
		if r.Code == "left" {
			found = true
		}
		if r.Code == "near_right" {
			t.Error("south-side stop must not serve the westbound direction")
		}
	}
	if !found {
		t.Error("north-side stop should serve the westbound direction")
	}
}

func TestBuildEndToEnd(t *testing.T) {
	// Round trip: east along 40.000, back west along 40.001 (one-way pair).
	lines := []RouteLine{{Number: "62", Parts: [][]geo.Point{{
		p(40, 49.00), p(40, 49.01), p(40, 49.02), p(40.001, 49.02), p(40.001, 49.01), p(40.001, 49.00),
	}}}, {Number: "99", Parts: [][]geo.Point{{p(41, 48), p(41, 48.01)}}}}
	var stops []Stop
	for i, lon := range []float64{49.002, 49.008, 49.014, 49.019} {
		stops = append(stops, Stop{Code: "e" + string(rune('0'+i)), Lat: 39.99990, Lon: lon}) // south side of eastbound
		stops = append(stops, Stop{Code: "w" + string(rune('0'+i)), Lat: 40.00110, Lon: lon}) // north side of westbound
	}
	built, skipped := Build(lines, stops, DefaultBuildConfig())
	if len(built) != 1 || len(skipped) != 1 || skipped[0].Number != "99" {
		t.Fatalf("built=%d skipped=%+v", len(built), skipped)
	}
	r := built[0]
	if !r.IsLoop || len(r.Stops0) != 4 || len(r.Stops1) != 4 {
		t.Fatalf("route: loop=%v stops0=%v stops1=%v", r.IsLoop, r.Stops0, r.Stops1)
	}
	if r.Stops0[0].Code != "e0" || r.Stops0[3].Code != "e3" || r.Stops1[0].Code != "w3" || r.Stops1[3].Code != "w0" {
		t.Errorf("order/direction wrong: %v / %v", r.Stops0, r.Stops1)
	}
}

func TestRouteID(t *testing.T) {
	for in, want := range map[string]string{"62": "bb_62", "7A": "bb_7a", "M 6": "bb_m_6", "149E": "bb_149e"} {
		if got := RouteID(in); got != want {
			t.Errorf("RouteID(%q) = %q, want %q", in, got, want)
		}
	}
}
