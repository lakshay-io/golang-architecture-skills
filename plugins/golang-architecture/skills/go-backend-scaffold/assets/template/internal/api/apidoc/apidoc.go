// Package apidoc describes the API in OpenAPI 3.1 from the code itself.
//
// Every handler documents the routes its Register mounts with a Docs method
// that returns a Group. api.Spec checks that the two list the same routes and
// Build turns the groups into docs/openapi.json. Request and response schemas
// are reflected from the handlers' own DTO types, so a renamed field or a
// changed type changes the spec on the next `make openapi`, and `make verify`
// fails until the new spec is committed. Every branch therefore carries the
// spec of the code on it.
package apidoc

import "slices"

// Documented is implemented by every handler: Docs describes, op for op, the
// routes its Register mounts.
type Documented interface {
	Docs() Group
}

// Public marks an op that needs no authentication in a group that does.
const Public = "-"

// Group documents the routes one handler mounts.
type Group struct {
	// Tag is the section the routes are listed under, e.g. "Orders", and
	// Description says what the section covers.
	Tag         string
	Description string
	// Auth names the security scheme (a key of Info.Security) every op needs
	// unless the op sets its own. Empty: the ops are public.
	Auth string
	// Headers are request headers every op reads.
	Headers []Param
	// Errors are failures every op can return, e.g. a rate limit on the group.
	Errors []Error
	Ops    []Op
}

// Op documents one route.
type Op struct {
	Method string
	// Path is the route as Register mounts it, relative to the router it is
	// given: "/orders/:id".
	Path string
	// Route is set only when the op documents one concrete value of a path
	// parameter: Path "/telemetry/location" documents Route "/telemetry/:type".
	// Several ops may document one route that way.
	Route string
	// ID is the operationId: unique and stable, in camelCase. Clients
	// generated from the spec name their methods after it. Derived from the
	// method and path when empty.
	ID          string
	Summary     string
	Description string
	// Auth overrides Group.Auth: a scheme name, or Public.
	Auth       string
	Deprecated bool
	// PathParams describe the path's parameters. A parameter not listed is
	// documented as a plain string.
	PathParams []Param
	Query      []Param
	Headers    []Param
	// Body is the request body: a DTO value (createOrderRequest{}), OneOf or
	// Fields. Nil: the route reads no body. Its schema comes from the type and
	// its example from the value, so fill the value in with realistic data.
	Body any
	// Example is the request body's example as JSON, when the Body value
	// cannot show it: a DTO with a custom JSON type needs one. Build checks it
	// against the Body's schema.
	Example string
	// Success lists every 2xx outcome. Outcomes with the same status merge
	// into one response whose code is one of theirs.
	Success []Success
	// Errors lists the failures the route itself returns. Build adds the
	// standard ones: the body errors when there is a Body, the auth scheme's,
	// the group's, 500 internal and 504 timeout.
	Errors []Error
}

// Param documents a path, query or header parameter. Every parameter is a
// string on the wire.
type Param struct {
	Name        string
	Description string
	Required    bool
	Enum        []string
	Pattern     string
	// Example is a realistic value, shown in the docs and used to fill in a
	// try-it request.
	Example string
}

// Success documents one 2xx outcome. The docs show it as a complete example
// response: the standard body with this status, code, message and data.
type Success struct {
	Status  int
	Code    string
	Message string
	// When says when this outcome happens, for routes with several.
	When string
	// Data is the DTO rendered as data: its schema comes from the type and its
	// example from the value. Nil documents the {} response.Success renders
	// for a nil result.
	Data any
	// Example is data's example as JSON, when the Data value cannot show it.
	// Build checks it against the Data's schema.
	Example string
	// Headers are response headers this outcome sets.
	Headers []Param
}

// Error documents one failure. The docs show it as a complete example
// response: the standard body with this status, code, message and details.
type Error struct {
	Status  int
	Code    string
	Message string
	When    string
	// Details is the example's details: field path to problem, for a failure
	// about specific fields.
	Details map[string]string
}

// Info describes the service.
type Info struct {
	Title       string
	Version     string
	Description string
	Servers     []Server
	// Security holds the bearer-token schemes ops refer to by key.
	Security map[string]Scheme
	// ResponseHeaders are headers every response carries. api.Spec adds the
	// security headers middleware.SecureHeaders sets.
	ResponseHeaders []Param
}

// Server is a base URL the API is served from.
type Server struct {
	URL         string
	Description string
}

// Scheme is an `Authorization: Bearer <token>` scheme.
type Scheme struct {
	Description string
	// Errors are the failures its middleware returns. Every op that uses the
	// scheme lists them.
	Errors []Error
}

// Mount is a group and the prefix it is mounted under: "" for the probes,
// "/v1" for the API.
type Mount struct {
	Prefix string
	Group  Group
}

// Schema is a JSON Schema (2020-12, the dialect OpenAPI 3.1 uses).
type Schema map[string]any

// Schemer is implemented by a type whose JSON differs from what its Go
// fields say, typically one with a custom UnmarshalJSON: it describes its own
// schema. Build refuses a type with custom JSON methods that does not.
type Schemer interface {
	OpenAPISchema() Schema
}

// OneOf documents a body or a result that takes one of several shapes.
func OneOf(variants ...any) any { return oneOf(variants) }

type oneOf []any

// Fields documents some of a DTO's fields, for routes that decode into one
// shared DTO but each accept different fields. Names are JSON names; Build
// fails on a name the DTO does not have.
func Fields(dto any, names ...string) FieldSet {
	return FieldSet{dto: dto, names: names}
}

// FieldSet is a subset of a DTO's fields; see Fields.
type FieldSet struct {
	dto      any
	names    []string
	required []string
	title    string
}

// Require marks fields of the set as required.
func (f FieldSet) Require(names ...string) FieldSet {
	f.required = append(slices.Clone(f.required), names...)
	return f
}

// Title names the shape, shown for a variant of OneOf.
func (f FieldSet) Title(title string) FieldSet {
	f.title = title
	return f
}
