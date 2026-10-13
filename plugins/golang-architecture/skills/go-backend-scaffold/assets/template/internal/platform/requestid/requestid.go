// Package requestid carries a per-request correlation id through context so
// logs, outbound calls and error responses can all be tied back to one request.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// Header is accepted from trusted upstreams (gateway, load balancer) and echoed back.
const Header = "X-Request-ID"

type ctxKey struct{}

func With(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func From(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}

// New returns a random 128-bit hex id.
func New() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails on supported platforms
	return hex.EncodeToString(b[:])
}

// Valid rejects ids that could pollute logs (too long or non-printable).
func Valid(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}
