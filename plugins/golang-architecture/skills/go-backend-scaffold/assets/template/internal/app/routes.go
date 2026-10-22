package app

import (
	"log/slog"

	"example.com/goservice/internal/api"
	"example.com/goservice/internal/api/handler"
	"example.com/goservice/internal/platform/health"
)

// services holds what the handlers are built from. New fills it; OpenAPI
// leaves it zero, because building the spec mounts the handlers but never
// calls them.
type services struct {
	readiness *health.Readiness
	log       *slog.Logger
	// orders *order.Service
}

// routes lists every handler the API mounts. New and OpenAPI both call it,
// so docs/openapi.json documents exactly the routes the service serves. A
// handler's constructor only stores what it is given: it must not call it.
func routes(s services) api.Routes {
	return api.Routes{
		Probes: []api.Registrar{handler.NewHealth(s.readiness, s.log)},
		V1:     []api.Registrar{
			// handler.NewOrderHandler(s.orders),
		},
	}
}
