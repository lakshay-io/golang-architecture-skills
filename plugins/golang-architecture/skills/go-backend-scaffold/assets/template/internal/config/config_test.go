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
		t.Fatal("New Relic must be off outside production")
	}
	if cfg.NewRelic.AppName != "goservice-local" {
		t.Fatalf("app name default: got %q", cfg.NewRelic.AppName)
	}
	if cfg.Lambda {
		t.Fatal("Lambda mode without AWS_LAMBDA_FUNCTION_NAME")
	}
}

func TestLambdaDetected(t *testing.T) {
	cfg, err := load(env(map[string]string{"AWS_LAMBDA_FUNCTION_NAME": "goservice-staging"}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Lambda {
		t.Fatal("AWS_LAMBDA_FUNCTION_NAME set, but Lambda is false")
	}
}

func TestLoadReportsEveryError(t *testing.T) {
	_, err := load(env(map[string]string{
		"HTTP_READ_TIMEOUT":   "soon",
		"HTTP_MAX_BODY_BYTES": "big",
		"LOG_LEVEL":           "loud",
		"SHUTDOWN_TIMEOUT":    "later",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
	for _, key := range []string{"HTTP_READ_TIMEOUT", "HTTP_MAX_BODY_BYTES", "LOG_LEVEL", "SHUTDOWN_TIMEOUT"} {
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
		{"production requires a license key", map[string]string{"APP_ENV": "production"}, "NEW_RELIC_LICENSE_KEY is required in production"},
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

// New Relic follows APP_ENV alone: a license key outside production, or the
// agent's own NEW_RELIC_ENABLED variable, never switches it on.
func TestNewRelicOnlyInProduction(t *testing.T) {
	tests := []struct {
		appEnv string
		want   bool
	}{
		{EnvProduction, true},
		{EnvStaging, false},
		{EnvTest, false},
		{EnvLocal, false},
	}
	for _, tc := range tests {
		t.Run(tc.appEnv, func(t *testing.T) {
			cfg, err := load(env(map[string]string{
				"APP_ENV":               tc.appEnv,
				"NEW_RELIC_LICENSE_KEY": "abc",
				"NEW_RELIC_ENABLED":     "true",
			}))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.NewRelic.Enabled != tc.want {
				t.Fatalf("APP_ENV=%s: enabled=%v, want %v", tc.appEnv, cfg.NewRelic.Enabled, tc.want)
			}
			if want := "goservice-" + tc.appEnv; cfg.NewRelic.AppName != want {
				t.Fatalf("app name: got %q, want %q", cfg.NewRelic.AppName, want)
			}
		})
	}
}

func TestLicenseKeyNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	slog.New(slog.NewJSONHandler(&buf, nil)).Info("cfg", slog.Any("newrelic", NewRelicConfig{LicenseKey: "secret-key"}))
	if strings.Contains(buf.String(), "secret-key") {
		t.Fatalf("license key leaked: %s", buf.String())
	}
}
