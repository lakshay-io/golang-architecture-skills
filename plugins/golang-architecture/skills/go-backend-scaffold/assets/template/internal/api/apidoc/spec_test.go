package apidoc_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"example.com/goservice/internal/api/apidoc"
)

type createOrder struct {
	Item string `json:"item" binding:"required,max=40"`
	Qty  int    `json:"qty" binding:"gte=1"`
}

type order struct {
	ID   string `json:"id"`
	Item string `json:"item"`
	Qty  int    `json:"qty"`
}

type pending struct {
	Ticket string `json:"ticket"`
}

var info = apidoc.Info{
	Title:   "orders",
	Version: "v1",
	Security: map[string]apidoc.Scheme{"userToken": {
		Description: "The user's token.",
		Errors:      []apidoc.Error{{Status: 401, Code: "unauthorized", Message: "Unauthorized", When: "No token, or a bad one."}},
	}},
	ResponseHeaders: []apidoc.Param{{Name: "Cache-Control", Required: true, Enum: []string{"no-store"}}},
}

func orders() apidoc.Group {
	return apidoc.Group{
		Tag: "Orders", Description: "Orders and their items.", Auth: "userToken",
		Headers: []apidoc.Param{{Name: "X-Tenant", Required: true, Example: "acme"}},
		Ops: []apidoc.Op{
			{
				Method: http.MethodPost, Path: "/orders", ID: "createOrder", Summary: "Create an order",
				Body: createOrder{Item: "socks", Qty: 2},
				Success: []apidoc.Success{
					{Status: 201, Code: "order_created", Message: "Order created", Data: order{ID: "o1", Item: "socks", Qty: 2}},
					{Status: 201, Code: "order_pending", When: "Stock is checked later.", Data: pending{Ticket: "t1"},
						Headers: []apidoc.Param{{Name: "Location", Description: "Where to poll."}}},
				},
				Errors: []apidoc.Error{{Status: 409, Code: "order_duplicate", When: "The same order twice.", Details: map[string]string{"item": "already ordered"}}},
			},
			{
				Method: http.MethodGet, Path: "/orders/:id", ID: "getOrder", Summary: "Get an order",
				PathParams: []apidoc.Param{{Name: "id", Pattern: "^o[0-9]+$", Example: "o1"}},
				Success:    []apidoc.Success{{Status: 200, Code: "order_found", Data: order{ID: "o1", Item: "socks", Qty: 2}}},
			},
			{
				Method: http.MethodGet, Path: "/orders/stats", ID: "orderStats", Auth: apidoc.Public,
				Success: []apidoc.Success{{Status: 200, Code: "stats_found"}},
			},
		},
	}
}

func build(t *testing.T, groups ...apidoc.Group) map[string]any {
	t.Helper()
	var mounts []apidoc.Mount
	for _, g := range groups {
		mounts = append(mounts, apidoc.Mount{Prefix: "/v1", Group: g})
	}
	raw, err := apidoc.Build(info, mounts)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("the spec is not JSON: %v", err)
	}
	return doc
}

// at walks a decoded document: at(doc, "paths", "/v1/orders", "post").
func at(t *testing.T, v any, keys ...string) any {
	t.Helper()
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok || m[k] == nil {
			t.Fatalf("no %q in %v", k, keys)
		}
		v = m[k]
	}
	return v
}

func TestBuildDocumentsEveryScenario(t *testing.T) {
	doc := build(t, orders())
	if doc["openapi"] != "3.1.0" {
		t.Fatalf("openapi = %v", doc["openapi"])
	}
	post := at(t, doc, "paths", "/v1/orders", "post")

	// The request: its example is the Body value.
	example := at(t, post, "requestBody", "content", "application/json", "example")
	if got, _ := json.Marshal(example); string(got) != `{"item":"socks","qty":2}` {
		t.Errorf("request example = %s", got)
	}

	// Both 201 outcomes merge into one response with an example each.
	created := at(t, post, "responses", "201", "content", "application/json")
	body := at(t, created, "examples", "order_created", "value").(map[string]any)
	if body["message"] != "Order created" || body["success"] != true || body["status"] != 201.0 {
		t.Errorf("order_created example = %v", body)
	}
	pendingBody := at(t, created, "examples", "order_pending", "value").(map[string]any)
	if pendingBody["message"] != "Order pending" {
		t.Errorf("a missing message reads the code: %v", pendingBody["message"])
	}
	if _, ok := at(t, created, "schema", "properties", "data").(map[string]any)["oneOf"]; !ok {
		t.Error("two data shapes at one status must be a oneOf")
	}
	if codes := at(t, created, "schema", "properties", "code", "enum"); len(codes.([]any)) != 2 {
		t.Errorf("code enum = %v", codes)
	}
	headers := at(t, post, "responses", "201", "headers").(map[string]any)
	for _, h := range []string{"X-Request-ID", "Cache-Control", "Location"} {
		if headers[h] == nil {
			t.Errorf("201 lacks the %s header", h)
		}
	}

	// Failures: the route's own, the scheme's and the body's, each with an
	// example; validation_failed because the body has binding rules.
	for status, codes := range map[string][]string{
		"400": {"invalid_json", "unknown_field", "duplicate_field", "validation_failed"},
		"401": {"unauthorized"},
		"409": {"order_duplicate"},
		"413": {"body_too_large"},
		"500": {"internal"},
		"504": {"timeout"},
	} {
		examples := at(t, post, "responses", status, "content", "application/json", "examples").(map[string]any)
		for _, code := range codes {
			if examples[code] == nil {
				t.Errorf("%s lacks a %s example", status, code)
			}
		}
	}
	dup := at(t, post, "responses", "409", "content", "application/json", "examples", "order_duplicate", "value").(map[string]any)
	if at(t, dup, "details", "item") != "already ordered" || dup["success"] != false || dup["data"] != nil {
		t.Errorf("failure example = %v", dup)
	}

	// Parameters: the path's, the group's headers and X-Request-ID.
	get := at(t, doc, "paths", "/v1/orders/{id}", "get")
	var names []string
	for _, p := range at(t, get, "parameters").([]any) {
		names = append(names, p.(map[string]any)["in"].(string)+":"+p.(map[string]any)["name"].(string))
	}
	if !slices.Equal(names, []string{"path:id", "header:X-Tenant", "header:X-Request-ID"}) {
		t.Errorf("parameters = %v", names)
	}
	if _, has := at(t, get, "responses").(map[string]any)["400"]; has {
		t.Error("a route without a body has no body errors")
	}

	// Security: the group's scheme, or explicitly none.
	if sec := at(t, get, "security").([]any); len(sec) != 1 {
		t.Errorf("security = %v", sec)
	}
	if sec := at(t, doc, "paths", "/v1/orders/stats", "get", "security").([]any); len(sec) != 0 {
		t.Errorf("a public op has empty security: %v", sec)
	}
	if at(t, doc, "paths", "/v1/orders/stats", "get", "responses", "200", "content", "application/json", "examples", "stats_found", "value", "data") == nil {
		t.Error("a nil Data is shown as {}")
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	mounts := []apidoc.Mount{{Prefix: "/v1", Group: orders()}}
	a, errA := apidoc.Build(info, mounts)
	b, errB := apidoc.Build(info, mounts)
	if errA != nil || errB != nil || !bytes.Equal(a, b) {
		t.Fatal("the same groups must give the same bytes")
	}
}

func TestBuildConcreteRoutes(t *testing.T) {
	g := apidoc.Group{Tag: "Telemetry", Ops: []apidoc.Op{
		{Method: http.MethodPost, Path: "/telemetry/battery", Route: "/telemetry/:type",
			Body:    apidoc.Fields(createOrder{Qty: 3}, "qty"),
			Success: []apidoc.Success{{Status: 202, Code: "batch_accepted"}}},
	}}
	doc := build(t, g)
	example := at(t, doc, "paths", "/v1/telemetry/battery", "post", "requestBody", "content", "application/json", "example")
	if got, _ := json.Marshal(example); string(got) != `{"qty":3}` {
		t.Errorf("a Fields example holds only its fields: %s", got)
	}
}

func TestBuildRefuses(t *testing.T) {
	op := func(edit func(*apidoc.Op)) apidoc.Group {
		o := apidoc.Op{Method: http.MethodGet, Path: "/x", ID: "x", Success: []apidoc.Success{{Status: 200, Code: "ok"}}}
		edit(&o)
		return apidoc.Group{Ops: []apidoc.Op{o}}
	}
	for name, c := range map[string]struct {
		groups []apidoc.Group
		want   string
	}{
		"duplicate operationId": {[]apidoc.Group{op(func(*apidoc.Op) {}), op(func(o *apidoc.Op) { o.Path = "/y" })}, "already used"},
		"unknown scheme":        {[]apidoc.Group{op(func(o *apidoc.Op) { o.Auth = "nope" })}, "not a scheme"},
		"unknown path param":    {[]apidoc.Group{op(func(o *apidoc.Op) { o.PathParams = []apidoc.Param{{Name: "id"}} })}, "has no :id"},
		"route mismatch":        {[]apidoc.Group{op(func(o *apidoc.Op) { o.Route = "/z/:id" })}, "not a value of route"},
		"no success":            {[]apidoc.Group{op(func(o *apidoc.Op) { o.Success = nil })}, "no Success"},
		"example with an unknown field": {[]apidoc.Group{op(func(o *apidoc.Op) {
			o.Method, o.Body, o.Example = http.MethodPost, createOrder{}, `{"item":"a","colour":"red"}`
		})}, `no field "colour"`},
		"example that is not JSON": {[]apidoc.Group{op(func(o *apidoc.Op) {
			o.Method, o.Body, o.Example = http.MethodPost, createOrder{}, `{"item":`
		})}, "not valid JSON"},
		"data example of the wrong type": {[]apidoc.Group{op(func(o *apidoc.Op) {
			o.Success = []apidoc.Success{{Status: 200, Code: "ok", Data: order{}, Example: `{"id":"o1","item":"a","qty":"two"}`}}
		})}, "not of type"},
	} {
		t.Run(name, func(t *testing.T) {
			var mounts []apidoc.Mount
			for _, g := range c.groups {
				mounts = append(mounts, apidoc.Mount{Prefix: "/v1", Group: g})
			}
			_, err := apidoc.Build(info, mounts)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}
