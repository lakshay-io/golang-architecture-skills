package apidoc

import (
	jsonv1 "encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

type address struct {
	City string `json:"city"`
}

type base struct {
	ID string `json:"id"`
}

type device struct {
	DeviceID string `json:"deviceId"`
}

type dto struct {
	base
	Name    string            `json:"name" binding:"required,min=1,max=40"`
	Kind    string            `json:"kind" binding:"omitempty,oneof=a b 'c d'"`
	Count   int               `json:"count" binding:"gte=1,lte=10"`
	Tags    []string          `json:"tags" binding:"max=3,dive,min=2"`
	Meta    map[string]int    `json:"meta,omitempty"`
	When    time.Time         `json:"when"`
	Home    *address          `json:"home"`
	Note    string            `json:"note,omitempty" doc:"Free text."`
	Raw     jsonv1.RawMessage `json:"raw"`
	Lat     float64           `json:"lat" binding:"latitude"`
	Must    string            `json:"must" api:"required"`
	Ignored string            `json:"-"`
	hidden  string
}

type inlined struct {
	device `json:",inline"`
	Name   string `json:"name"`
}

type numberish struct{}

func (n *numberish) UnmarshalJSON([]byte) error { return nil }
func (numberish) OpenAPISchema() Schema {
	return Schema{"type": []any{"number", "string"}, "description": "A number, or a numeric string."}
}

type withCustom struct {
	Lat numberish  `json:"lat"`
	Lng *numberish `json:"lng"`
}

type undescribed struct{}

func (u *undescribed) UnmarshalJSON([]byte) error { return nil }

type node struct {
	Next *node `json:"next"`
}

type left struct {
	Name string
}

type right struct {
	Name string
}

type ambiguous struct {
	left
	right
}

func reflectOf(t *testing.T, dir direction, v any) Schema {
	t.Helper()
	s, err := newReflector(dir).value(v)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func prop(t *testing.T, s Schema, name string) Schema {
	t.Helper()
	props, _ := s["properties"].(Schema)
	p, ok := props[name].(Schema)
	if !ok {
		t.Fatalf("no property %q in %v", name, s)
	}
	return p
}

func TestRequestSchema(t *testing.T) {
	s := reflectOf(t, decoded, dto{hidden: "not encoded"})

	props, _ := s["properties"].(Schema)
	var names []string
	for name := range props {
		names = append(names, name)
	}
	slices.Sort(names)
	want := []string{"count", "home", "id", "kind", "lat", "meta", "must", "name", "note", "raw", "tags", "when"}
	if !slices.Equal(names, want) {
		t.Fatalf("properties = %v, want %v (embedded flattened, - and unexported skipped)", names, want)
	}
	if got := s["required"]; !reflect.DeepEqual(got, []any{"name", "must"}) {
		t.Errorf("required = %v, want the binding:required and api:required fields", got)
	}
	if s["additionalProperties"] != false {
		t.Error("a request object must refuse other fields, as handler.BindJSON does")
	}
	for _, c := range []struct {
		field, key string
		want       any
	}{
		{"name", "minLength", int64(1)},
		{"name", "maxLength", int64(40)},
		{"kind", "enum", []any{"a", "b", "c d"}},
		{"count", "minimum", int64(1)},
		{"count", "maximum", int64(10)},
		{"tags", "maxItems", int64(3)},
		{"when", "format", "date-time"},
		{"note", "description", "Free text."},
		{"lat", "minimum", -90},
		{"lat", "maximum", 90},
		{"home", "type", []any{"object", "null"}},
	} {
		if got := prop(t, s, c.field)[c.key]; !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s.%s = %#v, want %#v", c.field, c.key, got, c.want)
		}
	}
	items, _ := prop(t, s, "tags")["items"].(Schema)
	if items["minLength"] != int64(2) {
		t.Errorf("rules after dive apply to each item: items = %v", items)
	}
	values, _ := prop(t, s, "meta")["additionalProperties"].(Schema)
	if values["type"] != "integer" {
		t.Errorf("map values = %v, want integer", values)
	}
	if raw := prop(t, s, "raw"); len(raw) != 0 {
		t.Errorf("a RawMessage takes any JSON value: %v", raw)
	}
}

func TestResponseSchemaRequiresEveryFieldThatIsAlwaysSent(t *testing.T) {
	s := reflectOf(t, encoded, dto{})
	required, _ := s["required"].([]any)
	for _, name := range []any{"id", "name", "kind", "count", "tags", "when", "home", "raw", "lat", "must"} {
		if !slices.Contains(required, name) {
			t.Errorf("%v is always sent, so it is required", name)
		}
	}
	for _, name := range []any{"meta", "note"} {
		if slices.Contains(required, name) {
			t.Errorf("%v is omitempty, so it is optional", name)
		}
	}
	if _, set := s["additionalProperties"]; set {
		t.Error("a response object stays open, so clients tolerate new fields")
	}
}

func TestInlineEmbedsAreFlattened(t *testing.T) {
	s := reflectOf(t, decoded, inlined{})
	prop(t, s, "deviceId")
	prop(t, s, "name")
}

func TestSchemerDescribesItself(t *testing.T) {
	s := reflectOf(t, decoded, withCustom{})
	if got := prop(t, s, "lat")["type"]; !reflect.DeepEqual(got, []any{"number", "string"}) {
		t.Errorf("lat = %v, want the type's own schema", got)
	}
	if got := prop(t, s, "lng")["type"]; !reflect.DeepEqual(got, []any{"number", "string", "null"}) {
		t.Errorf("lng = %v, want its own schema, nullable through the pointer", got)
	}
}

func TestReflectionRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		v    any
		want string
	}{
		"custom JSON without a schema": {undescribed{}, "implement OpenAPISchema()"},
		"recursive type":               {node{}, "recursive"},
		"ambiguous field":              {ambiguous{}, `named "Name"`},
		"unknown api tag": {struct {
			A string `json:"a" api:"optional"`
		}{}, "the only option is required"},
		"Fields of a missing field": {Fields(dto{}, "name", "nope"), `no field "nope"`},
		"Require outside the set":   {Fields(dto{}, "name").Require("count"), `Require("count")`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := newReflector(decoded).value(c.v)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one containing %q", err, c.want)
			}
		})
	}
}

func TestFieldsKeepsTheSelection(t *testing.T) {
	s := reflectOf(t, decoded, Fields(dto{}, "name", "count").Require("count").Title("small"))
	props, _ := s["properties"].(Schema)
	if len(props) != 2 || s["title"] != "small" {
		t.Fatalf("schema = %v, want name and count only, titled", s)
	}
	if got := s["required"]; !reflect.DeepEqual(got, []any{"name", "count"}) {
		t.Errorf("required = %v, want name (binding) and count (Require)", got)
	}
}

func TestRequiredPointersAreNotNullableInRequests(t *testing.T) {
	type point struct {
		Lat *float64 `json:"lat" api:"required"`
		Alt *float64 `json:"alt"`
	}
	req := reflectOf(t, decoded, point{})
	if got := prop(t, req, "lat")["type"]; got != "number" {
		t.Errorf("a required pointer refuses null in a request: lat = %v", got)
	}
	if got := prop(t, req, "alt")["type"]; !reflect.DeepEqual(got, []any{"number", "null"}) {
		t.Errorf("an optional pointer takes null: alt = %v", got)
	}
	if got := prop(t, reflectOf(t, encoded, point{}), "lat")["type"]; !reflect.DeepEqual(got, []any{"number", "null"}) {
		t.Errorf("a response pointer can be null: lat = %v", got)
	}
}

func TestConstFixesADiscriminator(t *testing.T) {
	s := reflectOf(t, decoded, Fields(dto{}, "name", "kind").Const("kind", "a"))
	kind := prop(t, s, "kind")
	if kind["const"] != "a" || kind["enum"] != nil {
		t.Errorf("kind = %v, want const a and no enum", kind)
	}
	if got := s["required"]; !reflect.DeepEqual(got, []any{"name", "kind"}) {
		t.Errorf("required = %v, want the fixed field too", got)
	}
	if _, err := newReflector(decoded).value(Fields(dto{}, "name").Const("kind", "a")); err == nil || !strings.Contains(err.Error(), `Const("kind")`) {
		t.Errorf("Const outside the set: err = %v", err)
	}
}

func TestExamplesAreCheckedAgainstTheSchema(t *testing.T) {
	s := reflectOf(t, decoded, address{})
	for raw, ok := range map[string]bool{
		`{"city":"Pune"}`:                true,
		`{"city":"Pune","country":"IN"}`: false, // a field the type does not have
		`{"city":3}`:                     false,
		`{}`:                             true,
		`not json`:                       false,
	} {
		if err := checkExample(s, []byte(raw)); (err == nil) != ok {
			t.Errorf("checkExample(%s) = %v, want ok=%v", raw, err, ok)
		}
	}
}
