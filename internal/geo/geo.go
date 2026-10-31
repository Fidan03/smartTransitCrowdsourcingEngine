// Package geo provides small geographic helpers: great-circle distance and
// distance-along-polyline operations used by the simulator and ETA.
package geo

import (
	"errors"
	"math"
)

const earthRadiusM = 6_371_000.0

type Point struct {
	Lat, Lon float64
}

func rad(d float64) float64 { return d * math.Pi / 180 }

// DistanceM returns the haversine distance between a and b in metres.
func DistanceM(a, b Point) float64 {
	dLat := rad(b.Lat - a.Lat)
	dLon := rad(b.Lon - a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

// Polyline is an ordered path with precomputed cumulative distances.
type Polyline struct {
	pts []Point
	cum []float64 // cum[i] = distance in metres from pts[0] to pts[i]
}

// NewPolyline requires at least two points.
func NewPolyline(pts []Point) (Polyline, error) {
	if len(pts) < 2 {
		return Polyline{}, errors.New("polyline needs at least two points")
	}
	cp := append([]Point(nil), pts...)
	cum := make([]float64, len(cp))
	for i := 1; i < len(cp); i++ {
		cum[i] = cum[i-1] + DistanceM(cp[i-1], cp[i])
	}
	return Polyline{pts: cp, cum: cum}, nil
}

func (p Polyline) LengthM() float64 { return p.cum[len(p.cum)-1] }

func (p Polyline) Points() []Point { return append([]Point(nil), p.pts...) }

// Reversed returns the same path traversed in the opposite direction.
func (p Polyline) Reversed() Polyline {
	rev := make([]Point, len(p.pts))
	for i, pt := range p.pts {
		rev[len(p.pts)-1-i] = pt
	}
	out, _ := NewPolyline(rev)
	return out
}

// PointAt returns the point d metres along the path, clamped to its ends.
func (p Polyline) PointAt(d float64) Point {
	if d <= 0 {
		return p.pts[0]
	}
	if d >= p.LengthM() {
		return p.pts[len(p.pts)-1]
	}
	for i := 1; i < len(p.pts); i++ {
		if d <= p.cum[i] {
			seg := p.cum[i] - p.cum[i-1]
			if seg == 0 {
				return p.pts[i]
			}
			t := (d - p.cum[i-1]) / seg
			a, b := p.pts[i-1], p.pts[i]
			return Point{Lat: a.Lat + t*(b.Lat-a.Lat), Lon: a.Lon + t*(b.Lon-a.Lon)}
		}
	}
	return p.pts[len(p.pts)-1]
}

// Project finds the closest point on the path to q. It returns the distance
// along the path to that point and the perpendicular offset from q, in metres.
// Uses a local equirectangular approximation, accurate at city scale.
func (p Polyline) Project(q Point) (alongM, offsetM float64) {
	best := math.Inf(1)
	for i := 1; i < len(p.pts); i++ {
		a, b := p.pts[i-1], p.pts[i]
		kx := math.Cos(rad(a.Lat)) * earthRadiusM * math.Pi / 180
		ky := earthRadiusM * math.Pi / 180
		bx, by := (b.Lon-a.Lon)*kx, (b.Lat-a.Lat)*ky
		qx, qy := (q.Lon-a.Lon)*kx, (q.Lat-a.Lat)*ky

		t := 0.0
		if l2 := bx*bx + by*by; l2 > 0 {
			t = math.Max(0, math.Min(1, (qx*bx+qy*by)/l2))
		}
		dx, dy := qx-t*bx, qy-t*by
		if off := math.Hypot(dx, dy); off < best {
			best = off
			alongM = p.cum[i-1] + t*(p.cum[i]-p.cum[i-1])
		}
	}
	return alongM, best
}
