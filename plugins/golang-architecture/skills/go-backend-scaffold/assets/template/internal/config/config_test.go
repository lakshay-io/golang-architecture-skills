package config

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(env(nil))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.HTTP.Addr != ":8080" || cfg.Log.Level != slog.LevelInfo || cfg.HTTP.RequestTimeout != 10*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if cfg.NewRelic.Enabled {
		t.Fatal("New Relic must default to off without a license key")
	}
	if cfg.NewRelic.AppName != "goservice-local" {
		t.Fatalf("app name default: got %q", cfg.NewRelic.AppName)
	}
}

func TestLoadReportsEveryError(t *testing.T) {
	_, err := load(env(map[string]string{
		"HTTP_READ_TIMEOUT":   "soon",
		"HTTP_MAX_BODY_BYTES": "big",
		"LOG_LEVEL":           "loud",
		"NEW_RELIC_ENABLED":   "maybe",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"HTTP_READ_TIMEOUT", "HTTP_MAX_BODY_BYTES", "LOG_LEVEL", "NEW_RELIC_ENABLED"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s: %v", key, err)
		}
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"request timeout above write timeout", map[string]string{"HTTP_REQUEST_TIMEOUT": "1m"}, "HTTP_REQUEST_TIMEOUT"},
		{"unknown env catches typos", map[string]string{"APP_ENV": "prod"}, "APP_ENV"},
		{"bad trusted proxy", map[string]string{"HTTP_TRUSTED_PROXIES": "10.0.0.0/8, lb.internal"}, "lb.internal"},
		{"enabled without key", map[string]string{"NEW_RELIC_ENABLED": "true"}, "NEW_RELIC_LICENSE_KEY"},
		{"production requires monitoring", map[string]string{"APP_ENV": "production"}, "production"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := load(env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error mentioning %q, got %v", tc.want, err)
			}
		})
	}
}

func TestNewRelicOnWhenKeyPresent(t *testing.T) {
	cfg, err := load(env(map[string]string{"NEW_RELIC_LICENSE_KEY": "abc", "APP_ENV": "production"}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.NewRelic.Enabled || cfg.NewRelic.AppName != "goservice-production" {
		t.Fatalf("unexpected: %+v", cfg.NewRelic)
	}
}

func TestLicenseKeyNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", slog.Any("newrelic", NewRelicConfig{LicenseKey: "secret-key"}))
	if strings.Contains(buf.String(), "secret-key") {
		t.Fatalf("license key leaked: %s", buf.String())
	}
}
