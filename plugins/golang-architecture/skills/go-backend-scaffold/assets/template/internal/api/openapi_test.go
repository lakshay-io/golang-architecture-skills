package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"example.com/goservice/internal/api"
	"example.com/goservice/internal/api/apidoc"
	"example.com/goservice/internal/api/handler"
)

// docRoutes mounts the routes given and documents the ops given, so a test
// can make the two agree or not.
type docRoutes struct {
	mount []string // "GET /x"
	ops   []apidoc.Op
}

func (d docRoutes) Register(r gin.IRouter) {
	for _, rt := range d.mount {
		method, path, _ := strings.Cut(rt, " ")
		r.Handle(method, path, handler.Wrap(func(*gin.Context) error { return nil }))
	}
}

func (d docRoutes) Docs() apidoc.Group { return apidoc.Group{Tag: "Test", Ops: d.ops} }

func getOp(path string) apidoc.Op {
	return apidoc.Op{Method: http.MethodGet, Path: path, Success: []apidoc.Success{{Status: 200, Code: "ok"}}}
}

var specInfo = apidoc.Info{Title: "t", Version: "v1"}

func TestSpecDocumentsWhatIsMounted(t *testing.T) {
	raw, err := api.Spec(specInfo, api.Routes{
		Probes: []api.Registrar{handler.NewHealth(nil, nil)},
		V1:     []api.Registrar{docRoutes{mount: []string{"GET /items/:id"}, ops: []apidoc.Op{getOp("/items/:id")}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Headers map[string]any `json:"headers"`
			} `json:"responses"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/readyz", "/v1/items/{id}"} {
		if doc.Paths[path] == nil {
			t.Errorf("the spec lacks %s", path)
		}
	}
	// Every response lists the headers middleware.SecureHeaders sets.
	headers := doc.Paths["/v1/items/{id}"]["get"].Responses["200"].Headers
	for _, h := range []string{"X-Request-ID", "Content-Security-Policy", "Strict-Transport-Security", "Cache-Control"} {
		if headers[h] == nil {
			t.Errorf("the 200 response lacks %s", h)
		}
	}
}

func TestSpecFailsWhenDocsAndRoutesDisagree(t *testing.T) {
	for name, c := range map[string]struct {
		reg  api.Registrar
		want string
	}{
		"a route without docs": {docRoutes{mount: []string{"GET /a", "POST /b"}, ops: []apidoc.Op{getOp("/a")}},
			"POST /v1/b is mounted but not documented"},
		"docs without a route": {docRoutes{mount: []string{"GET /a"}, ops: []apidoc.Op{getOp("/a"), getOp("/gone")}},
			"GET /v1/gone is documented but not mounted"},
		"no Docs method": {testRoutes{}, "has no Docs method"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := api.Spec(specInfo, api.Routes{V1: []api.Registrar{c.reg}})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestSpecAcceptsConcreteValuesOfOneRoute(t *testing.T) {
	battery, speed := getOp("/telemetry/battery"), getOp("/telemetry/speed")
	battery.Route, speed.Route = "/telemetry/:type", "/telemetry/:type"
	battery.ID, speed.ID = "battery", "speed"
	_, err := api.Spec(specInfo, api.Routes{V1: []api.Registrar{
		docRoutes{mount: []string{"GET /telemetry/:type"}, ops: []apidoc.Op{battery, speed}},
	}})
	if err != nil {
		t.Fatal(err)
	}
}
