package apidoc

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"example.com/goservice/internal/api/response"
)

// exampleRequestID is the requestId every example response shows.
const exampleRequestID = "b707e5a8f62f28f2f1d1b87312db24a6"

// namedExample is one example of a body: a variant's title (empty for the
// only one) and its JSON.
type namedExample struct {
	title string
	raw   []byte
}

// examples returns the JSON examples of a documented value. explicit, when
// given, is the example; otherwise it is the value itself, marshaled with
// encoding/json as Gin renders a response, one per OneOf variant. Each is
// checked against the value's schema, so an example cannot drift from its
// type.
func examples(v any, explicit string, s Schema) ([]namedExample, error) {
	if explicit != "" {
		raw := []byte(explicit)
		if err := checkExample(s, raw); err != nil {
			return nil, fmt.Errorf("example: %w", err)
		}
		return []namedExample{{raw: compact(raw)}}, nil
	}
	if variants, ok := v.(oneOf); ok {
		alts, _ := s["oneOf"].([]any)
		out := make([]namedExample, 0, len(variants))
		for i, variant := range variants {
			alt, _ := alts[i].(Schema)
			ex, err := examples(variant, "", alt)
			if err != nil {
				return nil, err
			}
			title, _ := alt["title"].(string)
			if title == "" {
				title = fmt.Sprintf("variant %d", i+1)
			}
			out = append(out, namedExample{title: title, raw: ex[0].raw})
		}
		return out, nil
	}
	raw, err := marshalExample(v)
	if err != nil {
		return nil, err
	}
	if err := checkExample(s, raw); err != nil {
		return nil, fmt.Errorf("example from the value: %w", err)
	}
	return []namedExample{{raw: raw}}, nil
}

// marshalExample marshals a documented value. A value whose JSON a type
// decides for itself cannot be shown this way: its Go value says nothing
// about the JSON the service reads.
func marshalExample(v any) ([]byte, error) {
	if f, ok := v.(FieldSet); ok {
		return marshalFields(f)
	}
	if t := customJSONIn(reflect.TypeOf(v), map[reflect.Type]bool{}); t != nil {
		return nil, fmt.Errorf("%s decides its own JSON, so the value cannot show the example: give Example as JSON", t)
	}
	return jsonv1.Marshal(v)
}

// marshalFields marshals the fields a FieldSet picked, in the DTO's order.
func marshalFields(f FieldSet) ([]byte, error) {
	t := reflect.TypeOf(f.dto)
	if c := customJSONIn(t, map[reflect.Type]bool{}); c != nil {
		return nil, fmt.Errorf("%s decides its own JSON, so the value cannot show the example: give Example as JSON", c)
	}
	raw, err := jsonv1.Marshal(f.dto)
	if err != nil {
		return nil, err
	}
	var all map[string]jsonv1.RawMessage
	if err := jsonv1.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fields, err := jsonFields(t)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteByte('{')
	for _, field := range fields {
		value, ok := all[field.name]
		if !ok || !slices.Contains(f.names, field.name) {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		name, _ := jsonv1.Marshal(field.name)
		b.Write(name)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// customJSONIn returns a type within t whose JSON it decides itself (a
// Schemer or a type with JSON methods), or nil.
func customJSONIn(t reflect.Type, seen map[reflect.Type]bool) reflect.Type {
	if t == nil || seen[t] {
		return nil
	}
	seen[t] = true
	if t == timeType || slices.Contains(rawTypes, t) {
		return nil
	}
	if implementsAny(t, []reflect.Type{schemerType}) || implementsAny(t, customJSON) {
		return t
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return customJSONIn(t.Elem(), seen)
	case reflect.Struct:
		for i := range t.NumField() {
			if c := customJSONIn(t.Field(i).Type, seen); c != nil {
				return c
			}
		}
	}
	return nil
}

// envelopeExample is a complete example response: the standard body as
// response.Success or response.Error renders it.
func envelopeExample(status int, code, message string, data []byte, details map[string]string) ([]byte, error) {
	body := response.Body{
		Success:   status < 400,
		Status:    status,
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: exampleRequestID,
	}
	if data != nil {
		body.Data = jsonv1.RawMessage(data)
	}
	return jsonv1.Marshal(body)
}

// sentence turns a code into a readable message for an example that gives
// none: "order_created" becomes "Order created".
func sentence(code string) string {
	s := strings.ReplaceAll(code, "_", " ")
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func compact(raw []byte) []byte {
	var b bytes.Buffer
	if err := jsonv1.Compact(&b, raw); err != nil {
		return raw
	}
	return b.Bytes()
}

// checkExample checks that raw is valid JSON that s accepts. Objects are
// checked strictly: an example may not carry a field its type does not have.
func checkExample(s Schema, raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	return errors.Join(check(s, v, "")...)
}

func check(s Schema, v any, path string) []error {
	at := path
	if at == "" {
		at = "the example"
	}
	if alts, ok := s["oneOf"].([]any); ok {
		return checkAlternatives(alts, v, at)
	}
	if alts, ok := s["anyOf"].([]any); ok {
		return checkAlternatives(alts, v, at)
	}
	if c, ok := s["const"]; ok && !sameJSON(c, v) {
		return []error{fmt.Errorf("%s: must be %v", at, c)}
	}
	if enum, ok := s["enum"].([]any); ok && !slices.ContainsFunc(enum, func(e any) bool { return sameJSON(e, v) }) {
		return []error{fmt.Errorf("%s: %v is not one of %v", at, v, enum)}
	}
	if !hasType(s, v) {
		return []error{fmt.Errorf("%s: %v is not of type %v", at, v, s["type"])}
	}
	var errs []error
	switch v := v.(type) {
	case map[string]any:
		props, _ := s["properties"].(Schema)
		extra, extraOK := s["additionalProperties"].(Schema)
		for name, field := range v {
			switch {
			case props[name] != nil:
				prop, _ := props[name].(Schema)
				errs = append(errs, check(prop, field, path+"."+name)...)
			case extraOK:
				errs = append(errs, check(extra, field, path+"."+name)...)
			case props != nil:
				errs = append(errs, fmt.Errorf("%s: the type has no field %q", at, name))
			}
		}
		required, _ := s["required"].([]any)
		for _, name := range required {
			if _, ok := v[name.(string)]; !ok {
				errs = append(errs, fmt.Errorf("%s: required field %q is missing", at, name))
			}
		}
	case []any:
		if items, ok := s["items"].(Schema); ok {
			for i, item := range v {
				errs = append(errs, check(items, item, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	}
	return errs
}

func checkAlternatives(alts []any, v any, at string) []error {
	for _, alt := range alts {
		if s, ok := alt.(Schema); ok && len(check(s, v, at)) == 0 {
			return nil
		}
	}
	return []error{fmt.Errorf("%s: matches none of its shapes", at)}
}

func hasType(s Schema, v any) bool {
	var types []string
	switch t := s["type"].(type) {
	case string:
		types = []string{t}
	case []any:
		for _, name := range t {
			if n, ok := name.(string); ok {
				types = append(types, n)
			}
		}
	default:
		return true // no type: any value
	}
	return slices.ContainsFunc(types, func(t string) bool {
		switch v := v.(type) {
		case nil:
			return t == "null"
		case bool:
			return t == "boolean"
		case string:
			return t == "string"
		case float64:
			return t == "number" || t == "integer" && v == float64(int64(v))
		case []any:
			return t == "array"
		case map[string]any:
			return t == "object"
		}
		return false
	})
}

func sameJSON(a, b any) bool {
	x, errA := jsonv1.Marshal(a)
	y, errB := jsonv1.Marshal(b)
	return errA == nil && errB == nil && bytes.Equal(x, y)
}
