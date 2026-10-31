// Package config loads application settings from environment variables,
// optionally pre-populated from a local .env file in development.
package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env              string // "development", "test", or "production"
	HTTPAddr         string
	DatabaseURL      string
	SimulatorEnabled bool

	SimulatorInterval time.Duration // how often simulated positions are emitted
	GPSStaleAfter     time.Duration // older positions are STALE (not shown as current)
	GPSMaxAge         time.Duration // older positions are ignored entirely (NO_DATA)

	// AlertSustain is how long reliable HIGH must persist before an alert.
	// Always 10m in production; may be shortened elsewhere for demos/tests.
	AlertSustain      time.Duration
	CrowdEvalInterval time.Duration // periodic crowd/alert re-evaluation
	ReportDupInterval time.Duration // min time between reports from one source per route

	ReportKeySecret []byte // HMAC key for anonymous submitter keys

	OperatorEmail    string // seeded operator account (created if missing)
	OperatorPassword string
}

func (c Config) IsProduction() bool { return c.Env == "production" }

// Load reads configuration from the environment. Values already present in the
// process environment take precedence over values in the .env file.
func Load(envFile string) (Config, error) {
	if envFile != "" {
		if err := LoadDotEnv(envFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read %s: %w", envFile, err)
		}
	}

	cfg := Config{
		Env:         getenv("APP_ENV", "development"),
		HTTPAddr:    getenv("HTTP_ADDR", ":8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}

	sim, err := strconv.ParseBool(getenv("SIMULATOR_ENABLED", "false"))
	if err != nil {
		return Config{}, fmt.Errorf("SIMULATOR_ENABLED: %w", err)
	}
	cfg.SimulatorEnabled = sim

	for _, d := range []struct {
		key      string
		fallback string
		dst      *time.Duration
	}{
		{"SIMULATOR_INTERVAL", "2s", &cfg.SimulatorInterval},
		{"GPS_STALE_AFTER", "60s", &cfg.GPSStaleAfter},
		{"GPS_MAX_AGE", "15m", &cfg.GPSMaxAge},
		{"ALERT_SUSTAIN", "10m", &cfg.AlertSustain},
		{"CROWD_EVAL_INTERVAL", "15s", &cfg.CrowdEvalInterval},
		{"REPORT_DUP_INTERVAL", "2m", &cfg.ReportDupInterval},
	} {
		v, err := time.ParseDuration(getenv(d.key, d.fallback))
		if err != nil || v <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration like 30s or 5m", d.key)
		}
		*d.dst = v
	}
	if cfg.GPSMaxAge < cfg.GPSStaleAfter {
		return Config{}, errors.New("GPS_MAX_AGE must be >= GPS_STALE_AFTER")
	}

	switch cfg.Env {
	case "development", "test", "production":
	default:
		return Config{}, fmt.Errorf("APP_ENV must be development, test, or production; got %q", cfg.Env)
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.IsProduction() && cfg.SimulatorEnabled {
		return Config{}, errors.New("SIMULATOR_ENABLED must be false in production")
	}
	if cfg.IsProduction() && cfg.AlertSustain != 10*time.Minute {
		return Config{}, errors.New("ALERT_SUSTAIN cannot be changed in production (the rule is 10 minutes)")
	}

	secret := os.Getenv("REPORT_KEY_SECRET")
	if len(secret) < 16 {
		if cfg.IsProduction() {
			return Config{}, errors.New("REPORT_KEY_SECRET (16+ chars) is required in production")
		}
		secret = "insecure-development-only-secret"
	}
	cfg.ReportKeySecret = []byte(secret)

	cfg.OperatorEmail = os.Getenv("OPERATOR_EMAIL")
	cfg.OperatorPassword = os.Getenv("OPERATOR_PASSWORD")
	return cfg, nil
}

func getenv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

// LoadDotEnv sets KEY=VALUE pairs from path without overriding existing vars.
func LoadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			return fmt.Errorf("line %d: expected KEY=VALUE", lineNo)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1] // quoted: keep contents verbatim
		} else if i := strings.Index(val, " #"); i >= 0 {
			val = strings.TrimSpace(val[:i]) // unquoted: strip inline comment
		}
		if _, exists := os.LookupEnv(key); !exists {
			os.Setenv(key, val)
		}
	}
	return sc.Err()
}
