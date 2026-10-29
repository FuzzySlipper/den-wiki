package registry

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
)

// schemaFor generates a JSON Schema object for an argument struct.
//
// Field tags:
//   - json:"name"       the field name (required; "-" skips the field)
//   - desc:"..."        the property description
//   - required:"true"   the field must be present
//   - enum:"a,b,c"      allowed string values
//   - min:"n" / max:"n" integer bounds
//
// The schema is closed (additionalProperties false), matching strict
// decoding.
func schemaFor(t reflect.Type) (map[string]any, []string) {
	props := map[string]any{}
	var required, fields []string
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			panic("registry: field " + t.Name() + "." + f.Name + " needs a json name")
		}
		p := typeSchema(f.Type)
		if d := f.Tag.Get("desc"); d != "" {
			p["description"] = d
		}
		if e := f.Tag.Get("enum"); e != "" {
			p["enum"] = strings.Split(e, ",")
		}
		if v := f.Tag.Get("min"); v != "" {
			p["minimum"] = json.Number(v)
		}
		if v := f.Tag.Get("max"); v != "" {
			p["maximum"] = json.Number(v)
		}
		props[name] = p
		fields = append(fields, name)
		if f.Tag.Get("required") == "true" {
			required = append(required, name)
		}
	}
	sort.Strings(fields)
	sort.Strings(required)
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s, fields
}

var rawMessage = reflect.TypeOf(json.RawMessage(nil))

func typeSchema(t reflect.Type) map[string]any {
	if t == rawMessage {
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return typeSchema(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int32, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": typeSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Interface:
		return map[string]any{}
	}
	panic("registry: unsupported argument type " + t.String())
}
