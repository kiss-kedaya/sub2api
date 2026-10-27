package basispoints

import "fmt"

func isCatalogItem(kind string) bool {
	return kind == "additional_tools" || kind == "tool_search_output"
}

type catalogDeclaration struct {
	item           object
	namespace, key string
	path           string
}

// Flatten only declaration identity here. Validate the selected schema later,
// so a superseded historical schema cannot reject a valid current declaration.
func flattenCatalogAt(value any, namespace, location string) ([]catalogDeclaration, error) {
	items, ok := value.([]any)
	if value != nil && !ok {
		return nil, fmt.Errorf("basispoints client tools must be an array")
	}
	var result []catalogDeclaration
	for index, raw := range items {
		itemPath := fmt.Sprintf("%s[%d]", location, index)
		item, ok := raw.(object)
		if !ok {
			return nil, fmt.Errorf("invalid Basispoints client tool")
		}
		kind, name := text(item["type"]), text(item["name"])
		if kind == "namespace" {
			if name == "" {
				return nil, fmt.Errorf("basispoints client namespaces require a name")
			}
			path := name
			if namespace != "" {
				path = namespace + "." + name
			}
			nested, err := flattenCatalogAt(item["tools"], path, itemPath+".tools")
			if err != nil {
				return nil, err
			}
			result = append(result, nested...)
			continue
		}
		key := name
		if namespace != "" {
			key = namespace + "." + name
		}
		if isUnsupportedHostedTool(kind) {
			key = "hosted:" + kind
		}
		result = append(result, catalogDeclaration{item: item, namespace: namespace, key: key, path: itemPath})
	}
	return result, nil
}

// Current explicit declarations win; without them, newer replay declarations
// replace inherited entries. Preserve first-seen order while choosing the last
// replayed definition. Cache publication still follows the existing CAS boundary.
func (b *Bridge) collectClientCatalog(source object, inherited bool) ([]any, error) {
	catalog, err := b.collectTools(source["tools"], "")
	if err != nil {
		return nil, err
	}
	var order []string
	replayed := make(map[string]catalogDeclaration)
	items, _ := source["input"].([]any)
	for index, raw := range items {
		item, _ := raw.(object)
		if !isCatalogItem(text(item["type"])) {
			continue
		}
		declarations, err := flattenCatalogAt(item["tools"], "", fmt.Sprintf("input[%d].tools", index))
		if err != nil {
			return nil, err
		}
		for _, declaration := range declarations {
			if _, exists := b.tools[declaration.key]; exists && !inherited {
				continue
			}
			if _, exists := replayed[declaration.key]; !exists {
				order = append(order, declaration.key)
			}
			replayed[declaration.key] = declaration
		}
	}
	if inherited {
		kept := catalog[:0]
		for _, raw := range catalog {
			entry, _ := raw.(object)
			if _, replaced := replayed[text(entry["name"])]; !replaced {
				kept = append(kept, raw)
			}
		}
		catalog = kept
	}
	for _, key := range order {
		declaration := replayed[key]
		if inherited {
			delete(b.tools, key)
		}
		additional, err := b.collectToolsAt([]any{declaration.item}, declaration.namespace, declaration.path, true)
		if err != nil {
			return nil, err
		}
		catalog = append(catalog, additional...)
	}
	return catalog, nil
}
