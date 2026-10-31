package geo

import (
	"math"
	"testing"
)

func near(a, b, tol float64) bool { return math.Abs(a-b) <= tol }

func TestDistanceOneDegreeLatitude(t *testing.T) {
	d := DistanceM(Point{40, 49}, Point{41, 49})
	if !near(d, 111_195, 50) {
		t.Fatalf("1 degree latitude = %.0f m, want ~111195", d)
	}
}

func TestPolylinePointAtAndLength(t *testing.T) {
	pl, err := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	if err != nil {
		t.Fatal(err)
	}
	seg1 := DistanceM(Point{40.0, 49.0}, Point{40.01, 49.0})
	if !near(pl.LengthM(), seg1+DistanceM(Point{40.01, 49.0}, Point{40.01, 49.01}), 0.001) {
		t.Fatal("length mismatch")
	}
	if p := pl.PointAt(-5); p != (Point{40.0, 49.0}) {
		t.Errorf("PointAt(<0) = %v", p)
	}
	if p := pl.PointAt(1e9); p != (Point{40.01, 49.01}) {
		t.Errorf("PointAt(>len) = %v", p)
	}
	mid := pl.PointAt(seg1 / 2)
	if !near(mid.Lat, 40.005, 1e-6) || mid.Lon != 49.0 {
		t.Errorf("midpoint = %v", mid)
	}
}

func TestProjectRoundTrip(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	for _, d := range []float64{0, 300, 1111, 1500, pl.LengthM()} {
		along, off := pl.Project(pl.PointAt(d))
		if !near(along, d, 2) || off > 2 {
			t.Errorf("Project(PointAt(%.0f)) = along %.1f, offset %.1f", d, along, off)
		}
	}
}

func TestProjectOffPath(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}})
	// ~85 m east of the midpoint of a north-south segment.
	along, off := pl.Project(Point{40.005, 49.001})
	if !near(along, pl.LengthM()/2, 2) || !near(off, 85, 3) {
		t.Errorf("along %.1f offset %.1f", along, off)
	}
}

func TestReversed(t *testing.T) {
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}, {40.01, 49.01}})
	r := pl.Reversed()
	if r.PointAt(0) != pl.PointAt(pl.LengthM()) || !near(r.LengthM(), pl.LengthM(), 1e-6) {
		t.Error("reversed polyline mismatch")
	}
}

func TestProjectSide(t *testing.T) {
	// Travelling north along longitude 49.0.
	pl, _ := NewPolyline([]Point{{40.0, 49.0}, {40.01, 49.0}})
	if _, _, side := pl.ProjectSide(Point{40.005, 49.0003}); side != 1 {
		t.Errorf("point east of a northbound line should be on the right, got %d", side)
	}
	if _, _, side := pl.ProjectSide(Point{40.005, 48.9997}); side != -1 {
		t.Errorf("point west of a northbound line should be on the left, got %d", side)
	}
	if _, _, side := pl.ProjectSide(Point{40.005, 49.0}); side != 0 {
		t.Errorf("point on the line should be 0, got %d", side)
	}
	if _, _, side := pl.Reversed().ProjectSide(Point{40.005, 49.0003}); side != -1 {
		t.Errorf("east point is on the left when travelling south, got %d", side)
	}
}

func TestSimplifyKeepsShapeDropsRedundantPoints(t *testing.T) {
	// An L-shape: 51 points north along a meridian, then 50 points east.
	var pts []Point
	for i := 0; i <= 50; i++ {
		pts = append(pts, Point{40 + float64(i)*0.0001, 49.0})
	}
	corner := len(pts) - 1
	for i := 1; i <= 50; i++ {
		pts = append(pts, Point{40.005, 49.0 + float64(i)*0.0001})
	}
	got := Simplify(pts, 5)
	if len(got) != 3 || got[0] != pts[0] || got[1] != pts[corner] || got[2] != pts[len(pts)-1] {
		t.Fatalf("Simplify kept %d points: %v", len(got), got)
	}
	// A huge tolerance reduces the L to its two ends.
	if len(Simplify(pts, 1000)) != 2 {
		t.Error("a 1 km tolerance should drop the corner")
	}
	if len(Simplify(pts[:2], 5)) != 2 {
		t.Error("two points must be kept as-is")
	}
	// Small wobbles below tolerance are removed.
	wobbly := []Point{{40, 49}, {40.001, 49.00002}, {40.002, 49}}
	if len(Simplify(wobbly, 5)) != 2 {
		t.Error("a ~2 m wobble should be simplified away at 5 m tolerance")
	}
}

func TestNewPolylineTooShort(t *testing.T) {
	if _, err := NewPolyline([]Point{{1, 1}}); err == nil {
		t.Fatal("expected error")
	}
}
