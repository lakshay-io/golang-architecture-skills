package apidoc

import (
	"encoding"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// direction is the way a value travels, which decides what its schema
// promises: a request is decoded strictly by handler.BindJSON, so its objects
// take no other fields and a field is required when its rules say so; a
// response is encoded as is, so a field is always there unless omitempty.
type direction int

const (
	decoded direction = iota // a request body, read by handler.BindJSON
	encoded                  // a response, written by response.Success and response.Error
)

var (
	schemerType = reflect.TypeFor[Schemer]()
	timeType    = reflect.TypeFor[time.Time]()
	rawTypes    = []reflect.Type{reflect.TypeFor[jsonv1.RawMessage](), reflect.TypeFor[jsontext.Value]()}
	// A type with one of these decides its own JSON: its Go fields say
	// nothing about it, so it must implement Schemer.
	customJSON = []reflect.Type{
		reflect.TypeFor[jsonv1.Marshaler](), reflect.TypeFor[jsonv1.Unmarshaler](),
		reflect.TypeFor[json.MarshalerTo](), reflect.TypeFor[json.UnmarshalerFrom](),
	}
	textTypes = []reflect.Type{reflect.TypeFor[encoding.TextMarshaler](), reflect.TypeFor[encoding.TextUnmarshaler]()}
)

type reflector struct {
	dir      direction
	visiting map[reflect.Type]bool
}

func newReflector(dir direction) *reflector {
	return &reflector{dir: dir, visiting: map[reflect.Type]bool{}}
}

// value returns the schema of a documented value: a DTO, OneOf or Fields.
func (r *reflector) value(v any) (Schema, error) {
	switch v := v.(type) {
	case oneOf:
		variants := make([]any, 0, len(v))
		for _, variant := range v {
			s, err := r.value(variant)
			if err != nil {
				return nil, err
			}
			if t := reflect.TypeOf(variant); t != nil && t.Name() != "" && s["title"] == nil {
				s["title"] = t.Name()
			}
			variants = append(variants, s)
		}
		return Schema{"oneOf": variants}, nil
	case FieldSet:
		return r.fieldSet(v)
	}
	t := reflect.TypeOf(v)
	if t == nil {
		return nil, errors.New("nil has no schema")
	}
	return r.of(t)
}

// of returns the schema of every value of type t.
func (r *reflector) of(t reflect.Type) (Schema, error) {
	if t.Kind() == reflect.Pointer {
		s, err := r.of(t.Elem())
		if err != nil {
			return nil, err
		}
		return nullable(s), nil
	}
	if s, ok := schemerOf(t); ok {
		return s, nil
	}
	if t == timeType {
		return Schema{"type": "string", "format": "date-time"}, nil
	}
	if slices.Contains(rawTypes, t) {
		return Schema{}, nil // any JSON value
	}
	if implementsAny(t, customJSON) {
		return nil, fmt.Errorf("%s has its own JSON methods: implement OpenAPISchema() to describe it", t)
	}
	if implementsAny(t, textTypes) {
		return Schema{"type": "string"}, nil
	}
	switch t.Kind() {
	case reflect.Bool:
		return Schema{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return Schema{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return Schema{"type": "number"}, nil
	case reflect.String:
		return Schema{"type": "string"}, nil
	case reflect.Interface:
		return Schema{}, nil
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return Schema{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := r.of(t.Elem())
		if err != nil {
			return nil, err
		}
		return Schema{"type": "array", "items": items}, nil
	case reflect.Map:
		values, err := r.of(t.Elem())
		if err != nil {
			return nil, err
		}
		return Schema{"type": "object", "additionalProperties": values}, nil
	case reflect.Struct:
		return r.object(t, nil)
	default:
		return nil, fmt.Errorf("%s: a %s has no JSON form", t, t.Kind())
	}
}

// fieldSet describes the fields Fields picked out of a DTO.
func (r *reflector) fieldSet(f FieldSet) (Schema, error) {
	t := reflect.TypeOf(f.dto)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("Fields needs a struct, not %v", t)
	}
	fields, err := jsonFields(t)
	if err != nil {
		return nil, err
	}
	var errs []error
	for _, name := range f.names {
		if !slices.ContainsFunc(fields, func(jf jsonField) bool { return jf.name == name }) {
			errs = append(errs, fmt.Errorf("Fields: %s has no field %q", t, name))
		}
	}
	for _, name := range f.required {
		if !slices.Contains(f.names, name) {
			errs = append(errs, fmt.Errorf("Fields: Require(%q) names a field the set does not list", name))
		}
	}
	for name := range f.consts {
		if !slices.Contains(f.names, name) {
			errs = append(errs, fmt.Errorf("Fields: Const(%q) names a field the set does not list", name))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	s, err := r.object(t, &f)
	if err != nil {
		return nil, err
	}
	if f.title != "" {
		s["title"] = f.title
	}
	return s, nil
}

// object describes a struct: one property per field encoding/json encodes.
// sel, when set, keeps only the fields Fields picked.
func (r *reflector) object(t reflect.Type, sel *FieldSet) (Schema, error) {
	if r.visiting[t] {
		return nil, fmt.Errorf("%s contains itself: a recursive type cannot be described", t)
	}
	r.visiting[t] = true
	defer delete(r.visiting, t)

	fields, err := jsonFields(t)
	if err != nil {
		return nil, err
	}
	props := Schema{}
	required := []any{}
	for _, f := range fields {
		if sel != nil && !slices.Contains(sel.names, f.name) {
			continue
		}
		req := r.required(f, sel)
		s, err := r.field(f, req)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", t, f.goName, err)
		}
		if v, fixed := sel.fixed(f.name); fixed {
			delete(s, "enum")
			s["const"] = v
		}
		props[f.name] = s
		if req {
			required = append(required, f.name)
		}
	}
	obj := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		obj["required"] = required
	}
	if r.dir == decoded {
		obj["additionalProperties"] = false
	}
	return obj, nil
}

// field describes one field. A required pointer field of a request is not
// nullable: the decoder reads null as nil, which the required rule refuses.
func (r *reflector) field(f jsonField, required bool) (Schema, error) {
	t := f.typ
	if r.dir == decoded && required {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
	}
	s, err := r.of(t)
	if err != nil {
		return nil, err
	}
	if f.asString && isScalar(f.typ) {
		s = Schema{"type": "string"}
	}
	if err := constrain(s, f.binding); err != nil {
		return nil, err
	}
	if f.doc != "" {
		s["description"] = f.doc
	}
	return s, nil
}

func (r *reflector) required(f jsonField, sel *FieldSet) bool {
	if sel != nil && slices.Contains(sel.required, f.name) {
		return true
	}
	if _, fixed := sel.fixed(f.name); fixed {
		return true
	}
	if r.dir == encoded {
		return !f.omitEmpty && !f.omitZero
	}
	return f.apiRequired || slices.Contains(fieldRules(f.binding), "required")
}

// jsonField is a struct field as encoding/json sees it.
type jsonField struct {
	name, goName        string
	typ                 reflect.Type
	omitEmpty, omitZero bool
	asString            bool
	binding, doc        string
	apiRequired         bool
	tagged              bool
	depth               int
}

// jsonFields lists the fields encoding/json encodes for t, in order, with
// embedded structs flattened. Of fields that share a name, the shallowest
// wins and, at equal depth, the one with a json tag; a tie is an error here,
// where encoding/json would silently drop both.
func jsonFields(t reflect.Type) ([]jsonField, error) {
	var all []jsonField
	if err := collect(t, 0, &all, map[reflect.Type]bool{t: true}); err != nil {
		return nil, err
	}
	byName := map[string][]jsonField{}
	var order []string
	for _, f := range all {
		if _, seen := byName[f.name]; !seen {
			order = append(order, f.name)
		}
		byName[f.name] = append(byName[f.name], f)
	}
	out := make([]jsonField, 0, len(order))
	for _, name := range order {
		f, err := dominant(t, byName[name])
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func collect(t reflect.Type, depth int, out *[]jsonField, seen map[reflect.Type]bool) error {
	for i := range t.NumField() {
		sf := t.Field(i)
		tag := sf.Tag.Get("json")
		if tag == "-" {
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		options := strings.Split(opts, ",")
		if sf.Anonymous {
			et := sf.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			// encoding/json inlines an embedded struct without a JSON name;
			// encoding/json/v2 also one tagged ",inline".
			if et.Kind() == reflect.Struct && (name == "" || slices.Contains(options, "inline")) {
				if seen[et] {
					continue
				}
				seen[et] = true
				err := collect(et, depth+1, out, seen)
				delete(seen, et)
				if err != nil {
					return err
				}
				continue
			}
		}
		if !sf.IsExported() {
			continue
		}
		if slices.Contains(options, "inline") {
			return fmt.Errorf("%s.%s: inline is supported only on an embedded struct", t, sf.Name)
		}
		apiRequired, err := parseAPITag(sf.Tag.Get("api"))
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t, sf.Name, err)
		}
		f := jsonField{
			name:        name,
			goName:      sf.Name,
			typ:         sf.Type,
			omitEmpty:   slices.Contains(options, "omitempty"),
			omitZero:    slices.Contains(options, "omitzero"),
			asString:    slices.Contains(options, "string"),
			binding:     sf.Tag.Get("binding"),
			doc:         sf.Tag.Get("doc"),
			apiRequired: apiRequired,
			tagged:      name != "",
			depth:       depth,
		}
		if f.name == "" {
			f.name = sf.Name
		}
		*out = append(*out, f)
	}
	return nil
}

func dominant(t reflect.Type, fs []jsonField) (jsonField, error) {
	minDepth := slices.MinFunc(fs, func(a, b jsonField) int { return a.depth - b.depth }).depth
	var shallow, tagged []jsonField
	for _, f := range fs {
		if f.depth == minDepth {
			shallow = append(shallow, f)
			if f.tagged {
				tagged = append(tagged, f)
			}
		}
	}
	switch {
	case len(shallow) == 1:
		return shallow[0], nil
	case len(tagged) == 1:
		return tagged[0], nil
	default:
		return jsonField{}, fmt.Errorf("%s: %d fields are named %q at the same depth, and encoding/json encodes none of them", t, len(shallow), shallow[0].name)
	}
}

// parseAPITag reads the `api` struct tag: "required" documents a field the
// domain requires, for DTOs validated there rather than by binding tags.
func parseAPITag(tag string) (required bool, err error) {
	if tag == "" {
		return false, nil
	}
	for opt := range strings.SplitSeq(tag, ",") {
		if opt != "required" {
			return false, fmt.Errorf("api tag %q: the only option is required", tag)
		}
		required = true
	}
	return required, nil
}

// fieldRules are a binding tag's rules for the field itself: those before a
// "dive", which apply to its items.
func fieldRules(binding string) []string {
	rules := strings.Split(binding, ",")
	if i := slices.Index(rules, "dive"); i >= 0 {
		rules = rules[:i]
	}
	return rules
}

// constrain adds the JSON Schema keywords that match go-playground/validator
// rules: the binding tag handler.BindJSON enforces. Rules before a "dive"
// constrain the field, rules after it each item. Rules with no JSON Schema
// equivalent are enforced but not described.
func constrain(s Schema, binding string) error {
	if binding == "" || binding == "-" {
		return nil
	}
	target := s
	inKeys := false
	for rule := range strings.SplitSeq(binding, ",") {
		switch {
		case rule == "keys":
			inKeys = true
			continue
		case rule == "endkeys":
			inKeys = false
			continue
		case inKeys || strings.Contains(rule, "|"):
			continue // map keys, and alternatives: not described
		case rule == "dive":
			items, ok := itemsOf(target)
			if !ok {
				return fmt.Errorf("binding %q: dive on a field that is neither a list nor a map", binding)
			}
			target = items
			continue
		}
		name, param, _ := strings.Cut(rule, "=")
		if err := applyRule(target, name, param); err != nil {
			return fmt.Errorf("binding %q: %w", binding, err)
		}
	}
	return nil
}

func applyRule(s Schema, name, param string) error {
	s = core(s)
	kind := baseType(s)
	switch name {
	case "min", "max", "len", "gt", "gte", "lt", "lte":
		n, err := strconv.ParseFloat(param, 64)
		if err != nil {
			return fmt.Errorf("%s=%s is not a number", name, param)
		}
		bound(s, kind, name, n)
	case "oneof":
		enum, err := enumValues(kind, param)
		if err != nil {
			return err
		}
		if isNullable(s) {
			enum = append(enum, nil)
		}
		s["enum"] = enum
	case "email":
		s["format"] = "email"
	case "url", "uri", "http_url":
		s["format"] = "uri"
	case "uuid", "uuid3", "uuid4", "uuid5", "uuid7":
		s["format"] = "uuid"
	case "hostname", "hostname_rfc1123":
		s["format"] = "hostname"
	case "ipv4":
		s["format"] = "ipv4"
	case "ipv6":
		s["format"] = "ipv6"
	case "latitude":
		s["minimum"], s["maximum"] = -90, 90
	case "longitude":
		s["minimum"], s["maximum"] = -180, 180
	case "e164":
		s["pattern"] = `^\+[1-9][0-9]{1,14}$`
	case "alphanum":
		s["pattern"] = `^[A-Za-z0-9]*$`
	case "numeric":
		s["pattern"] = `^[-+]?[0-9]+(\.[0-9]+)?$`
	case "hexadecimal":
		s["pattern"] = `^(0[xX])?[0-9a-fA-F]+$`
	}
	return nil
}

// bound turns a size rule into the keyword for the field's kind: validator
// measures a string by its length, a list by its items, a map by its keys and
// a number by its value.
func bound(s Schema, kind, rule string, n float64) {
	var lower, upper string
	switch kind {
	case "string":
		lower, upper = "minLength", "maxLength"
	case "array":
		lower, upper = "minItems", "maxItems"
	case "object":
		lower, upper = "minProperties", "maxProperties"
	case "integer", "number":
		switch rule {
		case "min", "gte":
			s["minimum"] = number(n)
		case "max", "lte":
			s["maximum"] = number(n)
		case "gt":
			s["exclusiveMinimum"] = number(n)
		case "lt":
			s["exclusiveMaximum"] = number(n)
		case "len":
			s["const"] = number(n)
		}
		return
	default:
		return
	}
	switch rule {
	case "min", "gte":
		s[lower] = number(n)
	case "max", "lte":
		s[upper] = number(n)
	case "gt":
		s[lower] = number(n + 1)
	case "lt":
		s[upper] = number(n - 1)
	case "len":
		s[lower], s[upper] = number(n), number(n)
	}
}

// enumValues splits a oneof parameter: values separated by spaces, a value
// with spaces in single quotes.
func enumValues(kind, param string) ([]any, error) {
	var words []string
	for rest := strings.TrimSpace(param); rest != ""; rest = strings.TrimSpace(rest) {
		if quoted, ok := strings.CutPrefix(rest, "'"); ok {
			word, after, found := strings.Cut(quoted, "'")
			if !found {
				return nil, fmt.Errorf("oneof=%s: unclosed quote", param)
			}
			words, rest = append(words, word), after
			continue
		}
		word, after, _ := strings.Cut(rest, " ")
		words, rest = append(words, word), after
	}
	enum := make([]any, 0, len(words))
	for _, w := range words {
		switch kind {
		case "integer", "number":
			n, err := strconv.ParseFloat(w, 64)
			if err != nil {
				return nil, fmt.Errorf("oneof=%s: %q is not a number", param, w)
			}
			enum = append(enum, number(n))
		default:
			enum = append(enum, w)
		}
	}
	return enum, nil
}

// number keeps whole numbers integral in the JSON output.
func number(n float64) any {
	if n == float64(int64(n)) {
		return int64(n)
	}
	return n
}

// nullable lets s also be null: a pointer encodes nil as null, and decodes
// null as nil.
func nullable(s Schema) Schema {
	if isNullable(s) || len(s) == 0 {
		return s // any value includes null already
	}
	switch t := s["type"].(type) {
	case string:
		s["type"] = []any{t, "null"}
	case []any:
		s["type"] = append(slices.Clone(t), "null")
	default:
		return Schema{"anyOf": []any{s, Schema{"type": "null"}}}
	}
	if enum, ok := s["enum"].([]any); ok {
		s["enum"] = append(enum, nil)
	}
	return s
}

func isNullable(s Schema) bool {
	switch t := s["type"].(type) {
	case []any:
		return slices.Contains(t, any("null"))
	case string:
		return t == "null"
	}
	if alts, ok := s["anyOf"].([]any); ok {
		return slices.ContainsFunc(alts, func(a any) bool { as, ok := a.(Schema); return ok && as["type"] == "null" })
	}
	return false
}

// core is the schema a rule constrains: s itself, or for a nullable s that
// wraps another schema, the non-null one.
func core(s Schema) Schema {
	if alts, ok := s["anyOf"].([]any); ok && len(alts) == 2 {
		if inner, ok := alts[0].(Schema); ok {
			return inner
		}
	}
	return s
}

// baseType is s's type without "null".
func baseType(s Schema) string {
	switch t := s["type"].(type) {
	case string:
		return t
	case []any:
		for _, v := range t {
			if name, ok := v.(string); ok && name != "null" {
				return name
			}
		}
	}
	return ""
}

func itemsOf(s Schema) (Schema, bool) {
	s = core(s)
	if items, ok := s["items"].(Schema); ok {
		return items, true
	}
	if values, ok := s["additionalProperties"].(Schema); ok {
		return values, true
	}
	return nil, false
}

// schemerOf calls t's OpenAPISchema, whether it has a value or a pointer
// receiver. The result is cloned, so a schema the method keeps in a variable
// is never changed by the caller.
func schemerOf(t reflect.Type) (Schema, bool) {
	var v reflect.Value
	switch {
	case t.Implements(schemerType):
		v = reflect.Zero(t)
	case reflect.PointerTo(t).Implements(schemerType):
		v = reflect.New(t)
	default:
		return nil, false
	}
	s, ok := v.Interface().(Schemer)
	if !ok {
		return nil, false
	}
	return maps.Clone(s.OpenAPISchema()), true
}

func implementsAny(t reflect.Type, ifaces []reflect.Type) bool {
	return slices.ContainsFunc(ifaces, func(i reflect.Type) bool {
		return t.Implements(i) || reflect.PointerTo(t).Implements(i)
	})
}

func isScalar(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}
