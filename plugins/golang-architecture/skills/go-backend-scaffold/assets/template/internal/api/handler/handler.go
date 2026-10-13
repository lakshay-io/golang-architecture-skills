// Package handler holds one Gin handler type per domain plus the shared
// plumbing (Wrap, binding). A handler only translates:
//
//	bind + validate the wire format → call the domain service → render
//
// Business rules live in internal/domain, never here.
//
// Two Gin rules every handler follows:
//   - Pass c.Request.Context() to services, not c. It carries the request
//     deadline and request id, and it is safe to hand to other goroutines.
//   - Never let *gin.Context escape the handler (goroutines, closures stored
//     for later): Gin pools and reuses it as soon as the handler returns.
package handler

import (
	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api/response"
)

// Wrap adapts an error-returning handler to Gin. Every failure path goes
// through response.Error, and a handler cannot forget to stop after writing
// an error — it just returns it.
func Wrap(h func(c *gin.Context) error) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := h(c); err != nil {
			response.Error(c, err)
		}
	}
}
