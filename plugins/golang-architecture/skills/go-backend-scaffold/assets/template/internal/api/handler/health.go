package handler

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api/response"
	"example.com/goservice/internal/platform/apperr"
	"example.com/goservice/internal/platform/health"
)

const (
	LivePath  = "/healthz"
	ReadyPath = "/readyz"
)

// ProbeRoutes are excluded from the access log.
var ProbeRoutes = []string{LivePath, ReadyPath}

var (
	errNotReady = apperr.Unavailable("not_ready", "Service is not ready")
	errDegraded = apperr.Unavailable("degraded", "A dependency is unavailable")
)

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
	response.Success(c, http.StatusOK, "live", "Service is running", nil)
}

func (h *Health) ready(c *gin.Context) {
	ok, failed := h.readiness.Status(c.Request.Context())
	switch {
	case ok:
		response.Success(c, http.StatusOK, "ready", "Service is ready", nil)
	case len(failed) == 0:
		response.Error(c, errNotReady)
	default:
		// Error text can name internal hosts: log it, return only check names.
		details := make(map[string]string, len(failed))
		for name, msg := range failed {
			details[name] = "failed"
			h.log.WarnContext(c.Request.Context(), "readiness check failed", slog.String("check", name), slog.String("err", msg))
		}
		response.Error(c, errDegraded.WithDetails(details))
	}
}
