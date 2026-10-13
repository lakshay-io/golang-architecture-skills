package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"example.com/goservice/internal/platform/requestid"
)

func TestForwardsRequestID(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = r.Header.Get(requestid.Header)
	}))
	defer srv.Close()

	ctx := requestid.With(context.Background(), "req-42")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := New(time.Second).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if got != "req-42" {
		t.Fatalf("downstream saw request id %q, want req-42", got)
	}
	if req.Header.Get(requestid.Header) != "" {
		t.Fatal("caller's request was mutated")
	}
}
