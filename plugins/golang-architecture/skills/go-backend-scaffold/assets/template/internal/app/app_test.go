package app_test

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"example.com/goservice/internal/app"
	"example.com/goservice/internal/config"
)

// The whole process boots, serves, reports ready and shuts down cleanly.
func TestLifecycle(t *testing.T) {
	t.Setenv("APP_ENV", config.EnvTest)
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "10ms")
	t.Setenv("SHUTDOWN_TIMEOUT", "5s")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	a, err := app.New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	select {
	case <-a.Started():
	case err := <-done:
		t.Fatalf("run exited early: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}

	// A client that leaves no connection behind. A keep-alive or speculatively
	// dialled connection that never sends a request makes http.Server.Shutdown
	// wait up to 5 s before treating it as idle, which would make this test flaky.
	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	base := "http://" + a.Addr().String()
	for path, want := range map[string]int{
		"/healthz":     http.StatusOK,
		"/readyz":      http.StatusOK,
		"/v1/no-route": http.StatusNotFound,
	} {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: got %d, want %d", path, resp.StatusCode, want)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown did not finish")
	}
}
