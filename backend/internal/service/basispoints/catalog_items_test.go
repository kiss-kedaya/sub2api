package basispoints

import (
	"encoding/json"
	"strings"
	"testing"
)

func catalogReadTool(kind string) object {
	return object{"type": "function", "name": "read_file", "parameters": object{"type": "object", "properties": object{"path": object{"type": kind}}, "required": []any{"path"}}}
}

func TestLoadedCatalogToolsAndSchemaPrecedence(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		source := testSource()
		old, current := catalogReadTool("number"), catalogReadTool("string")
		source["input"] = []any{object{"type": "additional_tools", "tools": []any{old}}, object{"type": "tool_search_output", "tools": []any{current}}, message("user", "continue")}
		if explicit {
			source["tools"] = []any{current}
			mustTestValue[[]any](t, source["input"])[1] = object{"type": "tool_search_output", "tools": []any{old}}
		}
		body, bridge := mustPrepare(t, source, "scope", nil)
		if len(bridge.tools) != 1 {
			t.Fatal("loaded declaration missing")
		}
		for _, raw := range mustTestValue[[]any](t, body["input"]) {
			item, _ := raw.(object)
			if isCatalogItem(text(item["type"])) {
				t.Fatal("catalog leaked into history")
			}
		}
		if _, err := bridge.translateCall(nativeCall(object{"name": "read_file", "arguments": object{"path": "file.go"}})); err != nil {
			t.Fatal(err)
		}
		if _, err := bridge.translateCall(nativeCall(object{"name": "read_file", "arguments": object{"path": 3}})); err == nil {
			t.Fatal("selected schema was not enforced")
		}
	}
}

func TestLoadedCatalogPersistsAndUpdatesScopedCache(t *testing.T) {
	cache := new(CatalogCache)
	source := testSource()
	source["input"] = []any{object{"type": "tool_search_output", "tools": []any{object{"type": "namespace", "name": "outer.inner", "tools": []any{catalogReadTool("number")}}}}, message("user", "continue")}
	prepareCatalogTest(t, cache, source, "scope")
	source["input"] = []any{object{"type": "tool_search_output", "tools": []any{object{"type": "namespace", "name": "outer.inner", "tools": []any{catalogReadTool("string")}}}}, message("user", "continue")}
	bridge := prepareCatalogTest(t, cache, source, "scope")
	call, err := bridge.translateCall(nativeCall(object{"name": "outer.inner.read_file", "arguments": object{"path": "file.go"}}))
	if err != nil || call["namespace"] != "outer.inner" || call["name"] != "read_file" {
		t.Fatalf("namespace/schema lost: %v %v", call, err)
	}
	source["input"] = "continue"
	if _, err := prepareCatalogTest(t, cache, source, "scope").translateCall(nativeCall(object{"name": "outer.inner.read_file", "arguments": object{"path": "file.go"}})); err != nil {
		t.Fatal(err)
	}
	if len(prepareCatalogTest(t, cache, source, "other").tools) != 0 {
		t.Fatal("catalog crossed scopes")
	}
}

func TestLoadedCatalogChoiceNoneAndInvalidSelectedSchema(t *testing.T) {
	source := testSource()
	source["tool_choice"] = "none"
	source["input"] = []any{object{"type": "tool_search_output", "tools": []any{catalogReadTool("string")}}, message("user", "continue")}
	body, bridge := mustPrepare(t, source, "scope", nil)
	if len(bridge.tools) != 0 {
		t.Fatal("choice none retained tools")
	}
	encoded, _ := json.Marshal(body)
	if strings.Contains(string(encoded), "tool_search_output") {
		t.Fatal("catalog leaked")
	}
	delete(source, "tool_choice")
	mustTestValue[object](t, mustTestValue[[]any](t, source["input"])[0])["tools"] = []any{object{"type": "function", "name": "read_file", "parameters": "invalid"}}
	raw, _ := json.Marshal(source)
	if _, _, err := Prepare(raw, "scope", nil); err == nil {
		t.Fatal("invalid selected schema accepted")
	}
}
