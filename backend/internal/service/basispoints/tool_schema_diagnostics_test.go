package basispoints

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func diagnosticRuleSchema(reference string) object {
	return object{"type": "object", "properties": object{"rule": object{
		"type": "object", "$defs": object{"ConditionGroup": object{"type": "string"}},
		"properties": object{"condition": object{"$ref": reference}},
	}}}
}

func prepareSchemaDiagnostic(t *testing.T, source object) *ToolSchemaValidationError {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = Prepare(raw, "schema-diagnostic", nil)
	var diagnostic *ToolSchemaValidationError
	if !errors.As(err, &diagnostic) {
		t.Fatalf("expected safe schema diagnostic, got %T: %v", err, err)
	}
	return diagnostic
}

func TestToolSchemaDiagnosticsIdentifyFailureAndCatalogLocation(t *testing.T) {
	cases := []struct {
		name, reason, pointer string
		schema                any
	}{
		{"dangling", "local_reference_not_found", "/properties/rule/properties/condition/$ref", diagnosticRuleSchema("#/$defs/ConditionGroup")},
		{"external", "external_reference_blocked", "/properties/rule/properties/condition/$ref", diagnosticRuleSchema("https://example.invalid/schema?token=PRIVATE_URL_SECRET")},
		{"file", "external_reference_blocked", "/$ref", object{"$ref": "file:///PRIVATE_FILE_SECRET"}},
		{"relative", "external_reference_blocked", "/$ref", object{"$ref": "private.json"}},
		{"anchor", "local_reference_not_found", "/$ref", object{"$ref": "#PRIVATE_MISSING_ANCHOR"}},
		{"dynamic", "local_reference_not_found", "/$dynamicRef", object{"$dynamicRef": "#PRIVATE_MISSING_ANCHOR"}},
		{"metaschema", "external_reference_blocked", "/$schema", object{"$schema": "https://example.invalid/PRIVATE_META_SECRET"}},
		{"structure", "invalid_schema", "/properties/value/type", object{"properties": object{"value": object{"type": "PRIVATE_TYPE_SECRET"}}}},
		{"non_object", "invalid_schema", "", "PRIVATE_SCHEMA_SECRET"},
	}
	for _, tc := range cases {
		for _, field := range []string{"parameters", "inputSchema", "input_schema"} {
			t.Run(tc.name+"/"+field, func(t *testing.T) {
				function := object{"type": "function", "name": "example_tool", field: tc.schema, "description": "PRIVATE_DESCRIPTION_SECRET"}
				source := testSource()
				source["tools"] = []any{
					object{"type": "function", "name": "valid_first"},
					object{"type": "namespace", "name": "example_mcp", "tools": []any{object{"type": "namespace", "name": "nested", "tools": []any{function}}}},
				}
				diagnostic := prepareSchemaDiagnostic(t, source)
				path := "tools[1].tools[0].tools[0]." + field
				if tc.pointer != "" {
					path += "#" + tc.pointer
				}
				if diagnostic.Reason != tc.reason || diagnostic.Path != path || diagnostic.Tool != "example_mcp.nested.example_tool" {
					t.Fatalf("wrong diagnostic: %#v", diagnostic)
				}
				if strings.Contains(diagnostic.Error(), "PRIVATE_") || strings.Contains(diagnostic.Error(), "https://") || strings.Contains(diagnostic.Error(), "file://") || errors.Unwrap(diagnostic) != nil {
					t.Fatalf("diagnostic leaked input: %s", diagnostic)
				}
			})
		}
	}
}

func TestToolSchemaDiagnosticsPreserveValidReferencesAndArgumentValidation(t *testing.T) {
	for _, schema := range []object{
		diagnosticRuleSchema("#/properties/rule/$defs/ConditionGroup"),
		{"$defs": object{"Condition": object{"$id": "https://example.invalid/embedded", "type": "string"}}, "properties": object{"rule": object{"type": "object", "properties": object{"condition": object{"$ref": "https://example.invalid/embedded"}}}}},
		{"$defs": object{"Condition": object{"$anchor": "condition", "type": "string"}}, "properties": object{"rule": object{"type": "object", "properties": object{"condition": object{"$ref": "#condition"}}}}},
	} {
		source := testSource()
		source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": schema}}
		_, bridge := mustPrepare(t, source, "scope", nil)
		for _, valid := range []bool{false, true} {
			var condition any = json.Number("42")
			if valid {
				condition = "allowed"
			}
			_, err := bridge.translateCall(nativeCall(object{"name": "example_tool", "arguments": object{"rule": object{"condition": condition}}}))
			if (err == nil) != valid {
				t.Fatalf("argument validation changed, valid=%t: %v", valid, err)
			}
		}
	}
}

func TestToolSchemaDiagnosticsLocateEmbeddedResourceAndIgnoreDataReferences(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": object{
		"default": object{"$ref": "https://example.invalid/missing"},
		"$defs":   object{"Rule": object{"$id": "https://example.invalid/local", "properties": object{"a/b~c": object{"$ref": "#/missing"}}}},
		"$ref":    "https://example.invalid/local",
	}}}
	diagnostic := prepareSchemaDiagnostic(t, source)
	if diagnostic.Reason != "local_reference_not_found" || diagnostic.Path != "tools[0].parameters#/$defs/Rule/properties/a~1b~0c/$ref" {
		t.Fatalf("embedded resource location lost: %#v", diagnostic)
	}
	// const/default/examples can contain arbitrary $ref-looking data. Only
	// the actual schema reference should be matched against the loader error.
	source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": object{
		"default":    object{"$ref": "https://example.invalid/missing"},
		"properties": object{"value": object{"$ref": "https://example.invalid/missing"}},
	}}}
	if got := prepareSchemaDiagnostic(t, source).Path; got != "tools[0].parameters#/properties/value/$ref" {
		t.Fatalf("data reference confused location: %s", got)
	}
}

func TestToolSchemaDiagnosticsKeepOriginalLoadedCatalogPositionAndPrecedence(t *testing.T) {
	for _, kind := range []string{"additional_tools", "tool_search_output"} {
		source := testSource()
		bad := object{"type": "function", "name": "example_tool", "parameters": object{"$ref": "#/missing"}}
		good := object{"type": "function", "name": "example_tool", "parameters": object{"type": "object"}}
		wrap := func(tool object) object {
			return object{"type": "namespace", "name": "example_mcp", "tools": []any{tool}}
		}
		source["input"] = []any{message("user", "offline"), object{"type": kind, "tools": []any{object{"type": "function", "name": "first"}, wrap(bad)}}}
		diagnostic := prepareSchemaDiagnostic(t, source)
		if diagnostic.Path != "input[1].tools[1].tools[0].parameters#/$ref" || diagnostic.Tool != "example_mcp.example_tool" {
			t.Fatalf("original replay position lost: %#v", diagnostic)
		}
		source["tools"] = []any{wrap(good)}
		mustPrepare(t, source, "scope", nil) // explicit current schema wins
		delete(source, "tools")
		source["input"] = append(mustTestValue[[]any](t, source["input"]), object{"type": kind, "tools": []any{wrap(good)}})
		mustPrepare(t, source, "scope", nil) // last replay schema wins
	}
}

func TestToolSchemaDiagnosticsDoNotLoadExternalResources(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, keyword := range []string{"$ref", "$schema"} {
		source := testSource()
		source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": object{keyword: server.URL + "/schema?token=PRIVATE_SECRET"}}}
		if got := prepareSchemaDiagnostic(t, source); got.Reason != "external_reference_blocked" {
			t.Fatalf("wrong external classification: %#v", got)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("schema compilation fetched an external URL")
	}
}

func TestToolSchemaDiagnosticsBoundAndRedactMetadata(t *testing.T) {
	for _, name := range []string{"bad\nPRIVATE_SECRET", "https://example.invalid/?token=PRIVATE_SECRET", strings.Repeat("x", 4096)} {
		source := testSource()
		source["tools"] = []any{object{"type": "function", "name": name, "parameters": object{"properties": object{"https://example.invalid/?token=PRIVATE_SECRET": object{"$ref": "#/PRIVATE_MISSING"}}}}}
		diagnostic := prepareSchemaDiagnostic(t, source)
		if diagnostic.Tool != "[redacted]" || !strings.Contains(diagnostic.Path, "[redacted]") || strings.Contains(diagnostic.Error(), "PRIVATE_") || len(diagnostic.Error()) > 2048 {
			t.Fatalf("unsafe metadata: %s", diagnostic)
		}
	}
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "large_schema", "parameters": object{"description": strings.Repeat("x", 1<<20)}}}
	diagnostic := prepareSchemaDiagnostic(t, source)
	if diagnostic.Tool != "large_schema" || diagnostic.Path != "tools[0].parameters" || diagnostic.Reason != "resource_limit" {
		t.Fatalf("resource limit lost tool context: %#v", diagnostic)
	}
}

func TestToolSchemaDiagnosticsFallBackWhenReferenceLocationIsAmbiguous(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": object{"properties": object{
		"first": object{"$ref": "#/missing"}, "second": object{"$ref": "#/missing"},
	}}}}
	diagnostic := prepareSchemaDiagnostic(t, source)
	if diagnostic.Path != "tools[0].parameters" || diagnostic.Reason != "local_reference_not_found" {
		t.Fatalf("ambiguous reference must retain safe tool location: %#v", diagnostic)
	}
}

func TestToolSchemaDiagnosticsIgnoreReferenceSiblingsInOlderDrafts(t *testing.T) {
	source := testSource()
	source["tools"] = []any{object{"type": "function", "name": "example_tool", "parameters": object{
		"$schema": "http://json-schema.org/draft-07/schema#",
		"properties": object{"value": object{
			"$ref":       "#/missing",
			"properties": object{"ignored": object{"$ref": "#/missing"}},
		}},
	}}}
	diagnostic := prepareSchemaDiagnostic(t, source)
	if diagnostic.Path != "tools[0].parameters#/properties/value/$ref" {
		t.Fatalf("ignored sibling confused reference location: %#v", diagnostic)
	}
}
