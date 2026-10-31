// Package seeds embeds demo data that is loaded only in development.
package seeds

import _ "embed"

// Demo is the simulated demo dataset (routes, stops, vehicles).
//
//go:embed demo.sql
var Demo string
