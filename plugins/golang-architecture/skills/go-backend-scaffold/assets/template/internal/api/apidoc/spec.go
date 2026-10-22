package apidoc

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"example.com/goservice/internal/api/response"
)

// The failures Build adds to the routes they can happen on, with the code and
// message handler.BindJSON, middleware.BodyLimit and response.Error give them.
var (
	bodyErrors = []Error{
		{Status: http.StatusBadRequest, Code: "invalid_json", Message: "request body is not valid JSON",
			When: "The body is not JSON, or data follows the object."},
		{Status: http.StatusBadRequest, Code: "invalid_json", Message: "request body must not be empty",
			When: "There is no body."},
		{Status: http.StatusBadRequest, Code: "invalid_json", Message: `field "qty" has the wrong type`,
			When: "A field has the wrong type: the message names it as a JSON pointer, e.g. points/0/lat."},
		{Status: http.StatusBadRequest, Code: "unknown_field", Message: `unknown field "colour"`,
			When: "A field the route does not take, or a key in another case."},
		{Status: http.StatusBadRequest, Code: "duplicate_field", Message: `field "name" appears more than once`,
			When: "A key appears twice in one object."},
		{Status: http.StatusRequestEntityTooLarge, Code: "body_too_large", Message: "request body must not exceed 1048576 bytes",
			When: "The body is larger than the service accepts."},
	}
	validationError = Error{Status: http.StatusBadRequest, Code: "validation_failed", Message: "request is invalid",
		When: "A field breaks its binding rules: details names each one.", Details: map[string]string{"name": "is required"}}
	everyRouteErrors = []Error{
		{Status: http.StatusInternalServerError, Code: "internal", Message: "internal error",
			When: "An unexpected failure. The message never says more: quote the requestId."},
		{Status: http.StatusGatewayTimeout, Code: "timeout", Message: "request timed out",
			When: "The request took longer than the service allows."},
	}
	requestIDParam = Param{
		Name:        "X-Request-ID",
		Description: "Optional: the caller's correlation id, up to 128 characters. The response echoes it, or a new one.",
		Example:     exampleRequestID,
	}
	requestIDHeader = Param{
		Name:        "X-Request-ID",
		Description: "The request's id: the caller's X-Request-ID, or a new one. Quote it when reporting a problem.",
		Required:    true,
		Example:     exampleRequestID,
	}
	methods = []string{"get", "put", "post", "delete", "patch", "head", "options"}
)

// Build renders the OpenAPI 3.1 document of the mounted groups. Every route
// shows its parameters and headers, its request body's schema with an
// example, and every response it can give, each scenario as a complete
// example body. The output is deterministic: the same code always gives the
// same bytes.
func Build(info Info, mounts []Mount) ([]byte, error) {
	if info.Title == "" || info.Version == "" {
		return nil, errors.New("apidoc: Info needs a Title and a Version")
	}
	b := &builder{info: info, paths: map[string]Schema{}, ids: map[string]string{}, tagDocs: map[string]string{}}
	var errs []error
	for _, m := range mounts {
		if m.Group.Tag != "" && !slices.Contains(b.tags, m.Group.Tag) {
			b.tags = append(b.tags, m.Group.Tag)
		}
		if m.Group.Description != "" && b.tagDocs[m.Group.Tag] == "" {
			b.tagDocs[m.Group.Tag] = m.Group.Description
		}
		for _, op := range m.Group.Ops {
			if err := b.add(m, op); err != nil {
				errs = append(errs, fmt.Errorf("%s %s%s: %w", op.Method, m.Prefix, op.Path, err))
			}
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	out, err := json.Marshal(b.document(), json.Deterministic(true), jsontext.WithIndent("  "))
	if err != nil {
		return nil, fmt.Errorf("apidoc: encode: %w", err)
	}
	return append(out, '\n'), nil
}

type builder struct {
	info  Info
	paths map[string]Schema
	ids   map[string]string // operationId → the route that took it
	tags  []string
	// tagDocs holds each tag's description.
	tagDocs map[string]string
}

func (b *builder) document() Schema {
	info := Schema{"title": b.info.Title, "version": b.info.Version}
	setIf(info, "description", b.info.Description)
	doc := Schema{"openapi": "3.1.0", "info": info, "paths": b.paths}
	if len(b.info.Servers) > 0 {
		servers := make([]any, 0, len(b.info.Servers))
		for _, s := range b.info.Servers {
			server := Schema{"url": s.URL}
			setIf(server, "description", s.Description)
			servers = append(servers, server)
		}
		doc["servers"] = servers
	}
	if len(b.tags) > 0 {
		tags := make([]any, 0, len(b.tags))
		for _, t := range b.tags {
			tag := Schema{"name": t}
			setIf(tag, "description", b.tagDocs[t])
			tags = append(tags, tag)
		}
		doc["tags"] = tags
	}
	if len(b.info.Security) > 0 {
		schemes := Schema{}
		for name, s := range b.info.Security {
			scheme := Schema{"type": "http", "scheme": "bearer"}
			setIf(scheme, "description", s.Description)
			schemes[name] = scheme
		}
		doc["components"] = Schema{"securitySchemes": schemes}
	}
	return doc
}

func (b *builder) add(m Mount, op Op) error {
	method := strings.ToLower(op.Method)
	if !slices.Contains(methods, method) {
		return fmt.Errorf("unknown method %q", op.Method)
	}
	if !strings.HasPrefix(op.Path, "/") {
		return errors.New("the path must start with /")
	}
	if op.Route != "" {
		if err := matchRoute(op.Path, op.Route); err != nil {
			return err
		}
	}
	if op.Example != "" && op.Body == nil {
		return errors.New("an Example is set but there is no Body")
	}
	path, names := openAPIPath(m.Prefix + op.Path)
	id := op.ID
	if id == "" {
		id = deriveID(op.Method, m.Prefix+op.Path)
	}
	if prev, taken := b.ids[id]; taken {
		return fmt.Errorf("operationId %q is already used by %s", id, prev)
	}
	b.ids[id] = op.Method + " " + path

	auth := op.Auth
	if auth == "" {
		auth = m.Group.Auth
	}
	if auth == Public {
		auth = ""
	}
	var scheme Scheme
	if auth != "" {
		s, ok := b.info.Security[auth]
		if !ok {
			return fmt.Errorf("auth %q is not a scheme in Info.Security", auth)
		}
		scheme = s
	}

	operation := Schema{"operationId": id}
	if m.Group.Tag != "" {
		operation["tags"] = []any{m.Group.Tag}
	}
	setIf(operation, "summary", op.Summary)
	setIf(operation, "description", op.Description)
	if op.Deprecated {
		operation["deprecated"] = true
	}
	if auth != "" {
		operation["security"] = []any{Schema{auth: []any{}}}
	} else {
		operation["security"] = []any{} // public, said explicitly
	}
	params, err := parameters(op, m.Group, names)
	if err != nil {
		return err
	}
	operation["parameters"] = params
	if op.Body != nil {
		body, err := requestBody(op)
		if err != nil {
			return fmt.Errorf("body: %w", err)
		}
		operation["requestBody"] = body
	}
	responses, err := b.responses(op, m.Group, auth != "", scheme)
	if err != nil {
		return err
	}
	operation["responses"] = responses

	ops, ok := b.paths[path]
	if !ok {
		ops = Schema{}
		b.paths[path] = ops
	}
	if _, dup := ops[method]; dup {
		return errors.New("documented twice")
	}
	ops[method] = operation
	return nil
}

func parameters(op Op, g Group, pathNames []string) ([]any, error) {
	for _, p := range op.PathParams {
		if !slices.Contains(pathNames, p.Name) {
			return nil, fmt.Errorf("PathParams: the path has no :%s", p.Name)
		}
	}
	params := []any{}
	for _, name := range pathNames {
		p := Param{Name: name}
		if i := slices.IndexFunc(op.PathParams, func(p Param) bool { return p.Name == name }); i >= 0 {
			p = op.PathParams[i]
		}
		p.Required = true
		params = append(params, parameter(p, "path"))
	}
	for _, p := range op.Query {
		params = append(params, parameter(p, "query"))
	}
	seen := map[string]bool{}
	for _, p := range slices.Concat(g.Headers, op.Headers, []Param{requestIDParam}) {
		key := strings.ToLower(p.Name)
		if seen[key] {
			return nil, fmt.Errorf("header %s is documented twice", p.Name)
		}
		seen[key] = true
		params = append(params, parameter(p, "header"))
	}
	return params, nil
}

func parameter(p Param, in string) Schema {
	out := Schema{"name": p.Name, "in": in, "required": p.Required, "schema": stringSchema(p)}
	setIf(out, "description", p.Description)
	setIf(out, "example", p.Example)
	return out
}

func stringSchema(p Param) Schema {
	s := Schema{"type": "string"}
	if len(p.Enum) > 0 {
		s["enum"] = toAny(p.Enum)
	}
	setIf(s, "pattern", p.Pattern)
	return s
}

// requestBody documents the body: its schema, reflected from the Body's type,
// and its example, from the Body's value or Example.
func requestBody(op Op) (Schema, error) {
	s, err := newReflector(decoded).value(op.Body)
	if err != nil {
		return nil, err
	}
	exs, err := examples(op.Body, op.Example, s)
	if err != nil {
		return nil, err
	}
	media := Schema{"schema": s}
	if len(exs) == 1 {
		media["example"] = jsontext.Value(exs[0].raw)
	} else {
		named := Schema{}
		for _, ex := range exs {
			named[ex.title] = Schema{"summary": ex.title, "value": jsontext.Value(ex.raw)}
		}
		media["examples"] = named
	}
	return Schema{"required": true, "content": Schema{"application/json": media}}, nil
}

// responses documents every outcome: the route's successes, then every
// failure it can return, merged by status.
func (b *builder) responses(op Op, g Group, authed bool, scheme Scheme) (Schema, error) {
	if len(op.Success) == 0 {
		return nil, errors.New("no Success documented")
	}
	out := Schema{}
	var statuses []int
	for _, s := range op.Success {
		if s.Status < 200 || s.Status > 299 {
			return nil, fmt.Errorf("success %q has status %d, not 2xx", s.Code, s.Status)
		}
		if !slices.Contains(statuses, s.Status) {
			statuses = append(statuses, s.Status)
		}
	}
	for _, status := range statuses {
		variants := slices.DeleteFunc(slices.Clone(op.Success), func(s Success) bool { return s.Status != status })
		r, err := b.successResponse(status, variants)
		if err != nil {
			return nil, err
		}
		out[strconv.Itoa(status)] = r
	}

	// The failures, by source, the route's own first. A code one source
	// documents replaces a later source's description of it (the route's own
	// over the standard one), while one source may document a code once per
	// scenario: the same code with another message or When.
	sources := [][]Error{op.Errors, g.Errors}
	if authed {
		sources = append(sources, scheme.Errors)
	}
	if op.Body != nil {
		sources = append(sources, bodyErrors)
		if hasBindingRules(op.Body) {
			sources = append(sources, []Error{validationError})
		}
	}
	sources = append(sources, everyRouteErrors)
	owner := map[string]int{} // code → the source that documents it
	byStatus := map[int][]Error{}
	var order []int
	for i, source := range sources {
		for _, e := range source {
			if e.Status < 400 || e.Status > 599 {
				return nil, fmt.Errorf("error %q has status %d, not 4xx or 5xx", e.Code, e.Status)
			}
			if e.Code == "" {
				return nil, fmt.Errorf("a %d error needs a Code", e.Status)
			}
			if first, seen := owner[e.Code]; seen && first != i {
				continue
			}
			owner[e.Code] = i
			if slices.ContainsFunc(byStatus[e.Status], func(x Error) bool { return x.Code == e.Code && x.When == e.When }) {
				return nil, fmt.Errorf("error %q is documented twice with the same When", e.Code)
			}
			if _, seen := byStatus[e.Status]; !seen {
				order = append(order, e.Status)
			}
			byStatus[e.Status] = append(byStatus[e.Status], e)
		}
	}
	for _, status := range order {
		r, err := b.errorResponse(status, byStatus[status])
		if err != nil {
			return nil, err
		}
		out[strconv.Itoa(status)] = r
	}
	return out, nil
}

// successResponse merges the successes that share a status into one
// response: its code is one of theirs, its data one of their shapes, and it
// carries one complete example body per success. A code may appear more than
// once, one per scenario (a list with rows and an empty one), told apart by
// When.
func (b *builder) successResponse(status int, variants []Success) (Schema, error) {
	var (
		codes []any
		lines []string
		datas []Schema
		types []reflect.Type
	)
	named := Schema{}
	headers := b.headers()
	for i, v := range variants {
		if v.Code == "" {
			return nil, fmt.Errorf("a %d success needs a Code", status)
		}
		if slices.ContainsFunc(variants[:i], func(x Success) bool { return x.Code == v.Code && x.When == v.When }) {
			return nil, fmt.Errorf("success %q is documented twice with the same When", v.Code)
		}
		if !slices.Contains(codes, any(v.Code)) {
			codes = append(codes, v.Code)
		}
		lines = append(lines, describe(v.Code, v.When))

		d, err := dataSchema(v.Data)
		if err != nil {
			return nil, fmt.Errorf("success %q data: %w", v.Code, err)
		}
		if !slices.ContainsFunc(datas, func(x Schema) bool { return reflect.DeepEqual(x, d) }) {
			datas = append(datas, d)
			types = append(types, reflect.TypeOf(v.Data))
		}
		ex, err := b.successExample(status, v, d, scenarios(variants, v.Code, func(s Success) string { return s.Code }) > 1)
		if err != nil {
			return nil, fmt.Errorf("success %q: %w", v.Code, err)
		}
		named[exampleKey(named, v.Code)] = ex

		for _, h := range v.Headers {
			headers[h.Name] = responseHeader(h, v.Code, variants)
		}
	}
	data := datas[0]
	if len(datas) > 1 {
		alts := make([]any, 0, len(datas))
		for i, d := range datas {
			if t := types[i]; t != nil && t.Name() != "" && d["title"] == nil {
				d["title"] = t.Name()
			}
			alts = append(alts, d)
		}
		data = Schema{"oneOf": alts}
	}
	body, err := envelope(status, codes, data)
	if err != nil {
		return nil, err
	}
	return Schema{
		"description": strings.Join(lines, "\n"),
		"headers":     headers,
		"content":     Schema{"application/json": Schema{"schema": body, "examples": named}},
	}, nil
}

func (b *builder) successExample(status int, v Success, d Schema, several bool) (Schema, error) {
	var data []byte
	if v.Data != nil || v.Example != "" {
		exs, err := examples(v.Data, v.Example, d)
		if err != nil {
			return nil, err
		}
		data = exs[0].raw
	} else {
		data = []byte("{}") // what response.Success renders for nil
	}
	msg := v.Message
	if msg == "" {
		msg = sentence(v.Code)
	}
	raw, err := envelopeExample(status, v.Code, msg, data, nil)
	if err != nil {
		return nil, err
	}
	return namedValue(v.Code, v.When, raw, several), nil
}

// errorResponse documents the failures that share a status: its code is one
// of theirs, and it carries one complete example body per failure.
func (b *builder) errorResponse(status int, list []Error) (Schema, error) {
	codes := make([]any, 0, len(list))
	lines := make([]string, 0, len(list))
	named := Schema{}
	for _, e := range list {
		if !slices.Contains(codes, any(e.Code)) {
			codes = append(codes, e.Code)
		}
		lines = append(lines, describe(e.Code, e.When))
		msg := e.Message
		if msg == "" {
			msg = sentence(e.Code)
		}
		raw, err := envelopeExample(status, e.Code, msg, nil, e.Details)
		if err != nil {
			return nil, err
		}
		several := scenarios(list, e.Code, func(x Error) string { return x.Code }) > 1
		named[exampleKey(named, e.Code)] = namedValue(e.Code, e.When, raw, several)
	}
	body, err := envelope(status, codes, nil)
	if err != nil {
		return nil, err
	}
	return Schema{
		"description": strings.Join(lines, "\n"),
		"headers":     b.headers(),
		"content":     Schema{"application/json": Schema{"schema": body, "examples": named}},
	}, nil
}

// headers are the headers every response carries.
func (b *builder) headers() Schema {
	out := Schema{requestIDHeader.Name: headerObject(requestIDHeader)}
	for _, h := range b.info.ResponseHeaders {
		out[h.Name] = headerObject(h)
	}
	return out
}

func headerObject(h Param) Schema {
	s := Schema{"schema": stringSchema(h), "required": h.Required}
	setIf(s, "description", h.Description)
	setIf(s, "example", h.Example)
	return s
}

func responseHeader(h Param, code string, variants []Success) Schema {
	always := !slices.ContainsFunc(variants, func(v Success) bool {
		return !slices.ContainsFunc(v.Headers, func(x Param) bool { return x.Name == h.Name })
	})
	if !always {
		h.Description = strings.TrimSpace(h.Description + " Sent with " + code + ".")
		h.Required = false
	}
	return headerObject(h)
}

// exampleKey names a response example: its code, then code-2, code-3 for
// the next scenarios with the same code.
func exampleKey(named Schema, code string) string {
	key := code
	for n := 2; named[key] != nil; n++ {
		key = fmt.Sprintf("%s-%d", code, n)
	}
	return key
}

// namedValue is one example: labelled by its code, and by its When too when
// the code has several scenarios, so the docs' example list tells them apart.
func namedValue(code, when string, raw []byte, several bool) Schema {
	summary := code
	if several && when != "" {
		summary = code + ": " + strings.TrimSuffix(when, ".")
	}
	s := Schema{"summary": summary, "value": jsontext.Value(raw)}
	setIf(s, "description", when)
	return s
}

// scenarios counts the entries documenting code.
func scenarios[T any](list []T, code string, codeOf func(T) string) int {
	n := 0
	for _, x := range list {
		if codeOf(x) == code {
			n++
		}
	}
	return n
}

// envelope is response.Body's schema for one status: the body every
// response has, reflected from the type response.Success and response.Error
// render, so the spec follows the body. A success carries data, a failure
// may carry details.
func envelope(status int, codes []any, data Schema) (Schema, error) {
	s, err := newReflector(encoded).of(reflect.TypeFor[response.Body]())
	if err != nil {
		return nil, err
	}
	props, _ := s["properties"].(Schema)
	required, _ := s["required"].([]any)
	want := []string{"success", "status", "code", "message", "data", "details", "requestId"}
	if len(props) != len(want) || slices.ContainsFunc(want, func(name string) bool { return props[name] == nil }) {
		return nil, fmt.Errorf("apidoc: response.Body no longer has exactly the fields %v: update envelope", want)
	}
	field := func(name string) Schema { f, _ := props[name].(Schema); return f }
	success := data != nil
	field("success")["const"] = success
	field("status")["const"] = status
	field("code")["enum"] = codes
	if success {
		if desc, ok := field("data")["description"].(string); ok && data["description"] == nil {
			data["description"] = desc
		}
		props["data"] = data
		required = append(required, "data")
		delete(props, "details")
	} else {
		delete(props, "data")
	}
	s["required"] = required
	return s, nil
}

// dataSchema is the schema of a success's data. Nil documents the {}
// response.Success renders for a nil result.
func dataSchema(v any) (Schema, error) {
	if v == nil {
		return Schema{"type": "object", "maxProperties": 0}, nil
	}
	return newReflector(encoded).value(v)
}

// hasBindingRules reports whether a body's type carries binding tags, the
// only rules that make handler.BindJSON answer validation_failed.
func hasBindingRules(v any) bool {
	switch v := v.(type) {
	case oneOf:
		return slices.ContainsFunc(v, hasBindingRules)
	case FieldSet:
		return hasBindingRules(v.dto)
	}
	return bindingIn(reflect.TypeOf(v), map[reflect.Type]bool{})
}

func bindingIn(t reflect.Type, seen map[reflect.Type]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return bindingIn(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			if tag := f.Tag.Get("binding"); tag != "" && tag != "-" || bindingIn(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

func describe(code, when string) string {
	if when == "" {
		return "- `" + code + "`"
	}
	return "- `" + code + "`: " + when
}

// openAPIPath turns a Gin path into an OpenAPI one: /orders/:id becomes
// /orders/{id}. It also returns the parameter names in order.
func openAPIPath(gin string) (string, []string) {
	segs := strings.Split(gin, "/")
	var names []string
	for i, seg := range segs {
		if seg != "" && (seg[0] == ':' || seg[0] == '*') {
			names = append(names, seg[1:])
			segs[i] = "{" + seg[1:] + "}"
		}
	}
	return strings.Join(segs, "/"), names
}

// matchRoute checks that path is one concrete value of route:
// /telemetry/location of /telemetry/:type.
func matchRoute(path, route string) error {
	p, r := strings.Split(path, "/"), strings.Split(route, "/")
	catchAll := strings.HasPrefix(r[len(r)-1], "*")
	ok := len(p) == len(r) || catchAll && len(p) > len(r)
	for i := 0; ok && i < len(r); i++ {
		if strings.HasPrefix(r[i], ":") || strings.HasPrefix(r[i], "*") {
			ok = p[i] != ""
		} else {
			ok = r[i] == p[i]
		}
	}
	if !ok {
		return fmt.Errorf("path %s is not a value of route %s", path, route)
	}
	return nil
}

// deriveID builds an operationId from a route: GET /v1/orders/:id becomes
// getV1OrdersById.
func deriveID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for seg := range strings.SplitSeq(path, "/") {
		if seg == "" {
			continue
		}
		if seg[0] == ':' || seg[0] == '*' {
			b.WriteString("By")
			seg = seg[1:]
		}
		for word := range strings.FieldsFuncSeq(seg, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
			b.WriteString(strings.ToUpper(word[:1]) + word[1:])
		}
	}
	return b.String()
}

func setIf(s Schema, key, value string) {
	if value != "" {
		s[key] = value
	}
}

func toAny(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}
