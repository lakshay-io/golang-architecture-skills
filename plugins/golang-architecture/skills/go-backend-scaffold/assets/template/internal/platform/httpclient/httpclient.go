// Package httpclient builds the outbound HTTP client that every adapter
// calling another service must use.
//
//   - Timeout: http.DefaultClient has none; one slow dependency would hold
//     goroutines and sockets until the process falls over.
//   - Pooling: the default transport keeps only 2 idle connections per host,
//     so under load most calls pay a fresh TCP + TLS handshake.
//   - Correlation: the inbound X-Request-ID is forwarded, and New Relic adds
//     distributed-tracing headers plus an external segment — as long as the
//     request is built with http.NewRequestWithContext(ctx, ...).
package httpclient

import (
	"net/http"
	"time"

	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/platform/requestid"
)

// New returns a client with an overall per-call timeout. Create one per
// downstream dependency at startup and reuse it: clients are safe for
// concurrent use, and reuse is what makes connection pooling work.
func New(timeout time.Duration) *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 200
	t.MaxIdleConnsPerHost = 64
	t.IdleConnTimeout = 90 * time.Second
	t.TLSHandshakeTimeout = 5 * time.Second

	return &http.Client{
		Timeout:   timeout,
		Transport: newrelic.NewRoundTripper(requestIDTransport{next: t}),
	}
}

type requestIDTransport struct{ next http.RoundTripper }

func (t requestIDTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if id := requestid.From(r.Context()); id != "" && r.Header.Get(requestid.Header) == "" {
		r = r.Clone(r.Context()) // a RoundTripper must not mutate the caller's request
		r.Header.Set(requestid.Header, id)
	}
	return t.next.RoundTrip(r)
}
