package config

import (
	"encoding/json"
	"reflect"
	"strings"
)

// JSONSchema returns a JSON schema for config.toml, for editors. It is built
// from the same structs Load decodes into, so the schema and the loader agree
// on every key. An editor that reads TOML against it flags the unknown keys
// Load would refuse.
func JSONSchema() []byte {
	schema := schemaOf(reflect.TypeFor[Config]())
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["title"] = "dispatch config.toml"
	b, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}

func schemaOf(t reflect.Type) map[string]any {
	switch t {
	case reflect.TypeFor[Duration]():
		return map[string]any{"type": "string", "pattern": `^(\d+d)?([0-9.]+(ns|us|µs|ms|s|m|h))*$`, "description": `A duration such as "5m" or "7d".`}
	case reflect.TypeFor[Rules]():
		return map[string]any{"type": "object", "description": "Your own settings, read by rules as ctx.config.rules. Dispatch reads only the five keys the shipped rules use.", "additionalProperties": true}
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64:
		return map[string]any{"type": "integer"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": schemaOf(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": schemaOf(t.Elem())}
	case reflect.Struct:
		props := map[string]any{}
		var required []string
		for i := range t.NumField() {
			name, _, _ := strings.Cut(t.Field(i).Tag.Get("toml"), ",")
			if name == "" {
				continue
			}
			props[name] = schemaOf(t.Field(i).Type)
		}
		if t == reflect.TypeFor[Repo]() {
			required = []string{"path"}
		}
		if t == reflect.TypeFor[Harness]() {
			required = []string{"kind", "model"}
		}
		if t == reflect.TypeFor[HTTPService]() {
			required = []string{"base_url"}
		}
		out := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			out["required"] = required
		}
		return out
	}
	return map[string]any{}
}
