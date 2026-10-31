// Package opendata imports real Baku public-transport open data from
// opendata.az: AYNA bus route paths (KMZ, CC0), BakuBus stop coordinates
// (CSV) and BakuBus daily ridership (CSV, CC BY).
package opendata

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"smart-transit/internal/geo"
)

// RouteLine is one route's raw geometry from the KML (possibly in pieces).
type RouteLine struct {
	Number string
	Parts  [][]geo.Point
}

// Stop is a BakuBus stop.
type Stop struct {
	Code string // official stop code, e.g. "001003892"
	Name string
	Lat  float64
	Lon  float64
	Type string // shelter type; "Təklif" means proposed, not built
}

// RidershipRow is one route's passenger trips on one day.
type RidershipRow struct {
	RouteNumber string
	Date        time.Time
	Trips       int
}

const maxKMLBytes = 200 << 20 // guard against decompression bombs

// ParseKMZ extracts and parses the first .kml file inside a KMZ archive.
func ParseKMZ(data []byte) ([]RouteLine, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open kmz: %w", err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".kml") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		kml, err := io.ReadAll(io.LimitReader(rc, maxKMLBytes+1))
		if err != nil {
			return nil, err
		}
		if len(kml) > maxKMLBytes {
			return nil, errors.New("kml too large")
		}
		return ParseKML(kml)
	}
	return nil, errors.New("no .kml file in kmz")
}

type kmlDoc struct {
	Placemarks []struct {
		Name  string `xml:"name"`
		Lines []struct {
			Coords string `xml:"coordinates"`
		} `xml:"MultiGeometry>LineString"`
		Line *struct {
			Coords string `xml:"coordinates"`
		} `xml:"LineString"`
	} `xml:"Document>Folder>Placemark"`
}

// ParseKML reads route placemarks. Placemarks sharing a route number are merged.
func ParseKML(data []byte) ([]RouteLine, error) {
	var doc kmlDoc
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parse kml: %w", err)
	}
	byNum := map[string]*RouteLine{}
	var order []string
	for _, pm := range doc.Placemarks {
		num := strings.TrimSpace(pm.Name)
		if num == "" {
			continue
		}
		coords := []string{}
		for _, l := range pm.Lines {
			coords = append(coords, l.Coords)
		}
		if pm.Line != nil {
			coords = append(coords, pm.Line.Coords)
		}
		rl, ok := byNum[num]
		if !ok {
			rl = &RouteLine{Number: num}
			byNum[num] = rl
			order = append(order, num)
		}
		for _, c := range coords {
			part, err := parseCoords(c)
			if err != nil {
				return nil, fmt.Errorf("route %s: %w", num, err)
			}
			if len(part) >= 2 {
				rl.Parts = append(rl.Parts, part)
			}
		}
	}
	out := make([]RouteLine, 0, len(order))
	for _, n := range order {
		if len(byNum[n].Parts) > 0 {
			out = append(out, *byNum[n])
		}
	}
	return out, nil
}

// parseCoords parses KML "lon,lat[,alt] lon,lat[,alt] ..." text.
func parseCoords(s string) ([]geo.Point, error) {
	fields := strings.Fields(s)
	pts := make([]geo.Point, 0, len(fields))
	for _, f := range fields {
		c := strings.Split(f, ",")
		if len(c) < 2 {
			return nil, fmt.Errorf("bad coordinate %q", f)
		}
		lon, err1 := strconv.ParseFloat(c[0], 64)
		lat, err2 := strconv.ParseFloat(c[1], 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("bad coordinate %q", f)
		}
		pts = append(pts, geo.Point{Lat: lat, Lon: lon})
	}
	return pts, nil
}

// inAzerbaijan rejects obviously wrong coordinates (swapped, zero, typos).
func inAzerbaijan(lat, lon float64) bool {
	return lat >= 38 && lat <= 42.5 && lon >= 44.5 && lon <= 51
}

// ProposedStopType marks planned stops that do not exist yet.
const ProposedStopType = "Təklif"

// ParseStops reads the BakuBus stops CSV (columns: ID, AD, ID, LONGITUDE,
// LATITUDE, Tip). Proposed stops, rows with invalid coordinates, and
// duplicate stop codes are skipped; the number skipped is returned.
func ParseStops(r io.Reader) (stops []Stop, skipped int, err error) {
	rows, err := readCSV(r)
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return nil, 0, errors.New("stops csv is empty")
	}
	seen := map[string]bool{}
	for _, row := range rows[1:] {
		if len(row) < 5 {
			skipped++
			continue
		}
		typ := ""
		if len(row) > 5 {
			typ = strings.TrimSpace(row[5])
		}
		lon, err1 := strconv.ParseFloat(strings.TrimSpace(row[3]), 64)
		lat, err2 := strconv.ParseFloat(strings.TrimSpace(row[4]), 64)
		code := strings.TrimSpace(row[2])
		if code == "" {
			code = "row" + strings.TrimSpace(row[0])
		}
		if err1 != nil || err2 != nil || !inAzerbaijan(lat, lon) ||
			strings.EqualFold(typ, ProposedStopType) || seen[code] {
			skipped++
			continue
		}
		seen[code] = true
		stops = append(stops, Stop{Code: code, Name: strings.TrimSpace(row[1]), Lat: lat, Lon: lon, Type: typ})
	}
	return stops, skipped, nil
}

// ParseRidership reads the BakuBus daily ridership CSV (columns: date
// "2026-01-01 00:00:00,000", route name, daily trips).
func ParseRidership(r io.Reader) ([]RidershipRow, error) {
	rows, err := readCSV(r)
	if err != nil {
		return nil, err
	}
	var out []RidershipRow
	for i, row := range rows {
		if i == 0 || len(row) < 3 {
			continue
		}
		ds := strings.TrimSpace(row[0])
		if len(ds) < 10 {
			continue
		}
		d, err := time.Parse("2006-01-02", ds[:10])
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(row[2]))
		if err != nil || n < 0 {
			continue
		}
		num := strings.TrimSpace(row[1])
		if num == "" {
			continue
		}
		out = append(out, RidershipRow{RouteNumber: num, Date: d, Trips: n})
	}
	if len(out) == 0 {
		return nil, errors.New("no ridership rows parsed")
	}
	return out, nil
}

func readCSV(r io.Reader) ([][]string, error) {
	b, err := io.ReadAll(io.LimitReader(r, 50<<20))
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")) // UTF-8 BOM
	cr := csv.NewReader(bytes.NewReader(b))
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	return cr.ReadAll()
}
