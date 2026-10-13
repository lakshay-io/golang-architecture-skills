package handler

import (
	"log/slog"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/platform/health"
)

const (
	LivePath  = "/healthz"
	ReadyPath = "/readyz"
)

// ProbeRoutes are excluded from the access log.
var ProbeRoutes = []string{LivePath, ReadyPath}

type Health struct {
	readiness *health.Readiness
	log       *slog.Logger
}

func NewHealth(r *health.Readiness, log *slog.Logger) *Health {
	return &Health{readiness: r, log: log}
}

func (h *Health) Register(r gin.IRouter) {
	r.GET(LivePath, h.live)
	r.GET(ReadyPath, h.ready)
}

// live never checks dependencies: a database outage must not make the
// orchestrator restart every replica at once.
func (h *Health) live(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Health) ready(c *gin.Context) {
	ok, failed := h.readiness.Status(c.Request.Context())
	switch {
	case ok:
		c.JSON(http.StatusOK, gin.H{"status": "ready"})
	case len(failed) == 0:
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready"})
	default:
		// Error text can name internal hosts: log it, return only check names.
		names := make([]string, 0, len(failed))
		for name, msg := range failed {
			names = append(names, name)
			h.log.WarnContext(c.Request.Context(), "readiness check failed", slog.String("check", name), slog.String("err", msg))
		}
		slices.Sort(names)
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "degraded", "failed": names})
	}
}
