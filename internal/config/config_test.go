package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{"APP_ENV", "HTTP_ADDR", "DATABASE_URL", "SIMULATOR_ENABLED",
		"SIMULATOR_INTERVAL", "GPS_STALE_AFTER", "GPS_MAX_AGE", "ALERT_SUSTAIN", "CROWD_EVAL_INTERVAL",
		"REPORT_DUP_INTERVAL", "REPORT_KEY_SECRET", "OPERATOR_EMAIL", "OPERATOR_PASSWORD"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
}

func TestLoadFromDotEnv(t *testing.T) {
	clearEnv(t)
	p := writeEnv(t, `
# comment
APP_ENV=development   # inline comment
DATABASE_URL="postgres://u:p#x@localhost/db"
SIMULATOR_ENABLED=true
`)
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.DatabaseURL != "postgres://u:p#x@localhost/db" {
		t.Errorf("DatabaseURL = %q", cfg.DatabaseURL)
	}
	if !cfg.SimulatorEnabled || cfg.HTTPAddr != ":8080" {
		t.Errorf("unexpected cfg: %+v", cfg)
	}
}

func TestProcessEnvOverridesDotEnv(t *testing.T) {
	clearEnv(t)
	t.Setenv("HTTP_ADDR", ":9999")
	p := writeEnv(t, "HTTP_ADDR=:8080\nDATABASE_URL=x\n")
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9999" {
		t.Errorf("HTTPAddr = %q, want :9999", cfg.HTTPAddr)
	}
}

func TestSimulatorForbiddenInProduction(t *testing.T) {
	clearEnv(t)
	p := writeEnv(t, "APP_ENV=production\nDATABASE_URL=x\nSIMULATOR_ENABLED=true\n")
	if _, err := Load(p); err == nil {
		t.Fatal("expected error when simulator enabled in production")
	}
}

func TestDurationDefaultsAndValidation(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeEnv(t, "DATABASE_URL=x\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SimulatorInterval != 2*time.Second || cfg.GPSStaleAfter != time.Minute || cfg.GPSMaxAge != 15*time.Minute {
		t.Errorf("unexpected defaults: %+v", cfg)
	}

	clearEnv(t)
	if _, err := Load(writeEnv(t, "DATABASE_URL=x\nGPS_STALE_AFTER=banana\n")); err == nil {
		t.Error("expected error for invalid duration")
	}
	clearEnv(t)
	if _, err := Load(writeEnv(t, "DATABASE_URL=x\nGPS_STALE_AFTER=10m\nGPS_MAX_AGE=1m\n")); err == nil {
		t.Error("expected error when max age < stale after")
	}
}

func TestAlertSustainLockedInProduction(t *testing.T) {
	clearEnv(t)
	cfg, err := Load(writeEnv(t, "DATABASE_URL=x\nALERT_SUSTAIN=2m\n"))
	if err != nil || cfg.AlertSustain != 2*time.Minute {
		t.Fatalf("dev override failed: %v %v", cfg.AlertSustain, err)
	}
	clearEnv(t)
	if _, err := Load(writeEnv(t, "APP_ENV=production\nDATABASE_URL=x\nREPORT_KEY_SECRET=0123456789abcdef\nALERT_SUSTAIN=2m\n")); err == nil {
		t.Fatal("production must refuse a changed ALERT_SUSTAIN")
	}
}

func TestReportSecretRequiredInProduction(t *testing.T) {
	clearEnv(t)
	if _, err := Load(writeEnv(t, "APP_ENV=production\nDATABASE_URL=x\n")); err == nil {
		t.Fatal("expected error without REPORT_KEY_SECRET in production")
	}
	clearEnv(t)
	cfg, err := Load(writeEnv(t, "APP_ENV=production\nDATABASE_URL=x\nREPORT_KEY_SECRET=0123456789abcdef\n"))
	if err != nil || cfg.AlertSustain != 10*time.Minute {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestDatabaseURLRequired(t *testing.T) {
	clearEnv(t)
	if _, err := Load(writeEnv(t, "APP_ENV=development\n")); err == nil {
		t.Fatal("expected error when DATABASE_URL missing")
	}
}
