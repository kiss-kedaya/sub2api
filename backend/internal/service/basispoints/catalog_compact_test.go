package basispoints

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCompactSchemaPreservesConstraintsAndFallbacks(t *testing.T) {
	schema := object{"type": "object", "properties": object{
		"path":  object{"type": "string", "minLength": 2, "pattern": "^/", "description": "Use the exact path."},
		"flags": object{"type": "array", "items": object{"type": "string", "enum": []any{"read", "check"}}, "minItems": 1},
	}, "required": []any{"path"}, "additionalProperties": false, "minProperties": 1}
	before := quoted(schema)
	got := describeSchema(schema, 0)
	for _, want := range []string{`"path":string`, `"flags"?:[string`, `"minLength":2`, `"pattern":"^/"`, `"enum":["read","check"]`, `"minItems":1`, `"minProperties":1`, "Use the exact path."} {
		if !strings.Contains(got, want) {
			t.Fatalf("constraint lost: %s", want)
		}
	}
	if strings.Contains(got, "...") || quoted(schema) != before {
		t.Fatal("additionalProperties or source changed")
	}
	for _, key := range []string{"$ref", "$defs", "oneOf", "allOf", "anyOf", "if", "prefixItems", "unevaluatedProperties"} {
		complex := object{"type": "object", key: object{"description": "preserve verbatim"}}
		if describeSchema(complex, 0) != quoted(complex) {
			t.Fatalf("complex schema %s must use full JSON", key)
		}
	}
	for _, edge := range []object{
		{"type": "object", "required": []any{"undeclared"}},
		{"type": "array", "items": []any{object{"type": "string"}}},
		{"type": []any{"string", "null"}},
	} {
		if describeSchema(edge, 0) != quoted(edge) {
			t.Fatal("unrepresentable schema must stay JSON")
		}
	}
	if got := describeSchema(object{"type": "object", "additionalProperties": object{"type": "integer", "minimum": 0}}, 0); !strings.Contains(got, `...:integer {"minimum":0}`) {
		t.Fatal(got)
	}
}

func TestCompactCatalogStillValidatesOriginalSchema(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "read", "parameters": object{"type": "object", "properties": object{"path": object{"type": "string", "pattern": "^/", "minLength": 3}}, "required": []any{"path"}, "additionalProperties": false}}}
	_, bridge := mustPrepare(t, source, "scope", nil)
	for _, args := range []object{{"path": "relative"}, {"path": "/"}, {"path": "/ok", "extra": true}, {}} {
		if _, err := bridge.translateCall(nativeCall(object{"name": "read", "arguments": args})); err == nil {
			t.Fatalf("invalid arguments accepted: %v", args)
		}
	}
	if _, err := bridge.translateCall(nativeCall(object{"name": "read", "arguments": object{"path": "/ok"}})); err != nil {
		t.Fatal(err)
	}
}

func TestCompactCatalogSyntheticMeasurement(t *testing.T) {
	// Deterministic synthetic catalog, not a user prompt or a billing measurement.
	var catalog []any
	for i := 0; i < 30; i++ {
		catalog = append(catalog, object{"type": "function", "name": fmt.Sprintf("read_%02d", i), "description": "Inspect a synthetic file.", "parameters": object{"type": "object", "properties": object{"path": object{"type": "string", "description": "Absolute file path."}, "limit": object{"type": "integer", "minimum": 1, "maximum": 1000}}, "required": []any{"path"}, "additionalProperties": false}})
	}
	raw, _ := json.Marshal(catalog)
	t.Logf("synthetic tools=%d input_schema_json_bytes=%d prompt_catalog_bytes=%d (includes notation)", len(catalog), len(raw), len(describeCatalog(catalog))+len(schemaNotation)+1)
}
