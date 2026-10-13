package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/newrelic/go-agent/v3/integrations/nrgin"
	"github.com/newrelic/go-agent/v3/newrelic"

	"example.com/goservice/internal/platform/requestid"
)

// NewRelic instruments each request as a New Relic web transaction named by
// route template ("GET /v1/orders/:id"), so thousands of distinct URLs roll up
// into one transaction per endpoint.
//
// Placement matters: after RequestID (so the transaction gets the request id)
// and BEFORE AccessLog — the agent silently drops logs recorded after a
// transaction has ended, so the access-log line must be written while it is
// still open to be linked to its trace.
//
// Not monitored: skipRoutes (health probes would dominate throughput and
// Apdex) and unmatched paths (scanners would mint junk transaction names).
// A nil app disables instrumentation (used by tests).
func NewRelic(app *newrelic.Application, skipRoutes ...string) gin.HandlersChain {
	skip := make(map[string]struct{}, len(skipRoutes))
	for _, r := range skipRoutes {
		skip[r] = struct{}{}
	}
	monitored := func(c *gin.Context) bool {
		route := c.FullPath()
		_, skipped := skip[route]
		return route != "" && !skipped
	}
	return gin.HandlersChain{
		nrgin.Middleware(app, nrgin.WithFilter(monitored)),
		transactionContext(),
	}
}

// transactionContext makes the transaction reachable through the request
// context — nrgin only stores it in Gin's key store — so the logger
// (logs in context), storage adapters (datastore segments) and outbound
// clients (distributed tracing) all find it with newrelic.FromContext(ctx).
//
// After the request it reports the real cause of a 5xx (recorded by
// response.Error) instead of a bare status code; see telemetry.NewRelic for
// the error-collection policy.
func transactionContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		txn := nrgin.Transaction(c)
		if txn == nil {
			c.Next()
			return
		}
		c.Request = newrelic.RequestWithTransactionContext(c.Request, txn)
		if id := requestid.From(c.Request.Context()); id != "" {
			txn.AddAttribute("request_id", id)
		}

		c.Next()

		if c.Writer.Status() >= http.StatusInternalServerError {
			if last := c.Errors.Last(); last != nil {
				txn.NoticeError(last.Err)
			}
		}
	}
}
