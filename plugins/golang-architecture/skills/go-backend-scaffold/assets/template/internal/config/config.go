// Package config loads and validates all runtime configuration once, at
// startup, from environment variables (12-factor). Nothing else in the
// codebase reads os.Getenv: dependencies receive typed values from here, and
// a bad deploy fails at boot with every problem listed, not at 3am on first use.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	EnvLocal      = "local"
	EnvTest       = "test"
	EnvStaging    = "staging"
	EnvProduction = "production"
)

var knownEnvs = []string{EnvLocal, EnvTest, EnvStaging, EnvProduction}

type Config struct {
	ServiceName string
	Env         string // local | test | staging | production
	Version     string // set at build/deploy time
	// Lambda is true inside AWS Lambda (the runtime sets
	// AWS_LAMBDA_FUNCTION_NAME): requests then arrive as API Gateway events
	// instead of on a socket. Locally and in a container it is false.
	Lambda bool

	HTTP     HTTPConfig
	Log      LogConfig
	Shutdown ShutdownConfig
	NewRelic NewRelicConfig
}

type HTTPConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration // slowloris guard
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	RequestTimeout    time.Duration // per-request context deadline
	MaxBodyBytes      int64
	// TrustedProxies are the load balancers allowed to set X-Forwarded-For.
	// Empty means trust none: the client IP is the TCP peer address.
	TrustedProxies []string
}

type LogConfig struct {
	Level  slog.Level
	Format string // json | text
}

type ShutdownConfig struct {
	// DrainDelay keeps serving after readiness flips to false, so the load
	// balancer stops routing new requests before connections are closed.
	DrainDelay time.Duration
	// Timeout bounds the whole graceful shutdown (drain + in-flight requests
	// + closing adapters). Keep it below the orchestrator's kill grace period.
	Timeout time.Duration
}

// NewRelicConfig holds the settings the service itself depends on. Every
// other standard NEW_RELIC_* variable (labels, log forwarding, high security,
// proxy, profiling...) is read by the agent directly; see .env.example.
type NewRelicConfig struct {
	Enabled    bool
	AppName    string
	LicenseKey string
}

// LogValue keeps the license key out of logs even if the config is logged whole.
func (c NewRelicConfig) LogValue() slog.Value {
	return slog.GroupValue(
		slog.Bool("enabled", c.Enabled),
		slog.String("app_name", c.AppName),
		slog.String("license_key", "[REDACTED]"),
	)
}

// Load reads configuration from the process environment.
func Load() (Config, error) { return load(os.LookupEnv) }

func load(lookup func(string) (string, bool)) (Config, error) {
	r := reader{lookup: lookup}

	service := r.str("SERVICE_NAME", "goservice")
	env := r.str("APP_ENV", EnvLocal)
	license := r.str("NEW_RELIC_LICENSE_KEY", "")

	cfg := Config{
		ServiceName: service,
		Env:         env,
		Version:     r.str("APP_VERSION", "dev"),
		Lambda:      r.str("AWS_LAMBDA_FUNCTION_NAME", "") != "",
		HTTP: HTTPConfig{
			Addr:              r.str("HTTP_ADDR", ":8080"),
			ReadHeaderTimeout: r.duration("HTTP_READ_HEADER_TIMEOUT", 5*time.Second),
			ReadTimeout:       r.duration("HTTP_READ_TIMEOUT", 15*time.Second),
			WriteTimeout:      r.duration("HTTP_WRITE_TIMEOUT", 30*time.Second),
			IdleTimeout:       r.duration("HTTP_IDLE_TIMEOUT", 120*time.Second),
			RequestTimeout:    r.duration("HTTP_REQUEST_TIMEOUT", 10*time.Second),
			MaxBodyBytes:      r.int64("HTTP_MAX_BODY_BYTES", 1<<20),
			TrustedProxies:    r.list("HTTP_TRUSTED_PROXIES"),
		},
		Log: LogConfig{
			Level:  r.logLevel("LOG_LEVEL", slog.LevelInfo),
			Format: r.str("LOG_FORMAT", "json"),
		},
		Shutdown: ShutdownConfig{
			DrainDelay: r.duration("SHUTDOWN_DRAIN_DELAY", 5*time.Second),
			Timeout:    r.duration("SHUTDOWN_TIMEOUT", 25*time.Second),
		},
		NewRelic: NewRelicConfig{
			// Monitoring runs in production only. Local, test and staging keep
			// the agent inert even when a license key is set, so they never
			// report into production dashboards or count against usage.
			Enabled:    env == EnvProduction,
			AppName:    r.str("NEW_RELIC_APP_NAME", service+"-"+env),
			LicenseKey: license,
		},
	}
	if err := errors.Join(r.errs...); err != nil {
		return Config{}, err
	}
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) validate() error {
	var errs []error
	if c.ServiceName == "" {
		errs = append(errs, errors.New("SERVICE_NAME must not be empty"))
	}
	if !slices.Contains(knownEnvs, c.Env) {
		errs = append(errs, fmt.Errorf("APP_ENV must be one of %v, got %q", knownEnvs, c.Env))
	}
	if c.Log.Format != "json" && c.Log.Format != "text" {
		errs = append(errs, fmt.Errorf("LOG_FORMAT must be json or text, got %q", c.Log.Format))
	}
	if c.HTTP.MaxBodyBytes <= 0 {
		errs = append(errs, errors.New("HTTP_MAX_BODY_BYTES must be positive"))
	}
	if c.HTTP.RequestTimeout >= c.HTTP.WriteTimeout {
		errs = append(errs, errors.New("HTTP_REQUEST_TIMEOUT must be shorter than HTTP_WRITE_TIMEOUT, or clients get a dropped connection instead of a 504"))
	}
	for _, p := range c.HTTP.TrustedProxies {
		if net.ParseIP(p) == nil {
			if _, _, err := net.ParseCIDR(p); err != nil {
				errs = append(errs, fmt.Errorf("HTTP_TRUSTED_PROXIES: %q is not an IP or CIDR", p))
			}
		}
	}
	if c.Shutdown.DrainDelay >= c.Shutdown.Timeout {
		errs = append(errs, errors.New("SHUTDOWN_DRAIN_DELAY must be shorter than SHUTDOWN_TIMEOUT"))
	}
	if c.NewRelic.Enabled && c.NewRelic.LicenseKey == "" {
		errs = append(errs, errors.New("NEW_RELIC_LICENSE_KEY is required in production"))
	}
	return errors.Join(errs...)
}

// reader collects every parse error instead of stopping at the first, so a
// misconfigured deploy reports all its problems in one go.
type reader struct {
	lookup func(string) (string, bool)
	errs   []error
}

func (r *reader) raw(key string) (string, bool) {
	v, ok := r.lookup(key)
	v = strings.TrimSpace(v)
	return v, ok && v != ""
}

func (r *reader) str(key, def string) string {
	if v, ok := r.raw(key); ok {
		return v
	}
	return def
}

func (r *reader) list(key string) []string {
	v, ok := r.raw(key)
	if !ok {
		return nil
	}
	var out []string
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (r *reader) duration(key string, def time.Duration) time.Duration {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return d
}

func (r *reader) int64(key string, def int64) int64 {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return n
}

func (r *reader) logLevel(key string, def slog.Level) slog.Level {
	v, ok := r.raw(key)
	if !ok {
		return def
	}
	var l slog.Level
	if err := l.UnmarshalText([]byte(v)); err != nil {
		r.errs = append(r.errs, fmt.Errorf("%s: %w", key, err))
		return def
	}
	return l
}
